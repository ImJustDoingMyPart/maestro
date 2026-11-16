package hub

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
	"github.com/ImJustDoingMyPart/maestro/internal/telegram"
)

func TestWorthAlert(t *testing.T) {
	tests := []struct {
		from, to status.State
		want     bool
	}{
		{status.Up, status.Down, true},
		{status.Degraded, status.Down, true},
		{"", status.Down, true}, // nunca visto, y ya caído
		{status.Down, status.Up, true},
		{status.Down, status.Degraded, true},
		{status.Down, status.Stopped, true},
		{status.Down, status.Down, false}, // cambió el motivo, sigue caído
		{status.Up, status.Degraded, false},
		{status.Degraded, status.Up, false},
		{status.Up, status.Stopped, false},
		{"", status.Up, false},
	}
	for _, tt := range tests {
		if got := worthAlert(tt.from, tt.to); got != tt.want {
			t.Errorf("worthAlert(%q, %q) = %v", tt.from, tt.to, got)
		}
	}
}

func TestAlertOn(t *testing.T) {
	ui := map[string]bool{"service:p/a": false, "service:p/b": true, "found:vm/unit/x.service": false}
	tests := []struct {
		name string
		svc  catalog.Service
		want bool
	}{
		{"por defecto", catalog.Service{ID: "p/c", Alert: true}, true},
		{"la interfaz lo apaga", catalog.Service{ID: "p/a", Alert: true}, false},
		{"la interfaz lo prende", catalog.Service{ID: "p/b"}, true},
		{"el archivo gana a la interfaz", catalog.Service{ID: "p/a", Alert: true, AlertFromFile: true}, true},
		{"descubierto, con su clave", catalog.Service{ID: "found:vm/unit/x.service", Alert: true}, false},
	}
	for _, tt := range tests {
		if got := alertOn(tt.svc, ui); got != tt.want {
			t.Errorf("%s: alertOn = %v", tt.name, got)
		}
	}
}

func TestAlertText(t *testing.T) {
	loc := time.FixedZone("ART", -3*3600)
	at := time.Date(2026, 11, 16, 17, 32, 0, 0, time.UTC) // 14:32 en Argentina
	tests := []struct {
		name string
		ev   store.StateEvent
		link string
		want string
	}{
		{
			"check que falla",
			store.StateEvent{At: at, From: "degraded", To: "down", Reason: "check http: timeout"},
			"https://m.example.org/services/immich/immich-server",
			"🔴 <b>Immich &lt;x&gt;</b> dejó de responder\n🕗 14:32 · 💬 check http: timeout\n🔗 <a href=\"https://m.example.org/services/immich/immich-server\">Ver en Maestro</a>",
		},
		{
			"contenedor detenido, sin link",
			store.StateEvent{At: at, From: "up", To: "down", Reason: "app detenido (exit 137)"},
			"",
			"🔴 <b>Immich &lt;x&gt;</b> se cayó\n🕗 14:32 · 💬 app detenido (exit 137)\n<i>Maestro</i>",
		},
		{
			"vuelve",
			store.StateEvent{At: at, From: "down", To: "up", Reason: "volvió a responder"},
			"",
			"✅ <b>Immich &lt;x&gt;</b> volvió a funcionar\n🕗 14:32\n<i>Maestro</i>",
		},
		{
			"vuelve degradado",
			store.StateEvent{At: at, From: "down", To: "degraded", Reason: "check http: código 503 (1 falla)"},
			"",
			"🟡 <b>Immich &lt;x&gt;</b> volvió, con problemas\n🕗 14:32 · 💬 check http: código 503 (1 falla)\n<i>Maestro</i>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := alertText("Immich <x>", tt.ev, loc, tt.link); got != tt.want {
				t.Errorf("alertText:\n got %q\nwant %q", got, tt.want)
			}
		})
	}

	long := strings.Repeat("ñ", 1000)
	got := alertText("x", store.StateEvent{At: at, To: "down", Reason: long}, loc, "")
	if n := strings.Count(got, "ñ"); n != maxReason-1 || !strings.Contains(got, "…") {
		t.Errorf("motivo largo: %d ñ", n)
	}
}

func TestAlertName(t *testing.T) {
	for _, tt := range []struct {
		svc  catalog.Service
		want string
	}{
		{catalog.Service{Source: catalog.SourceCompose, Project: "immich", Name: "immich-server"}, "immich-server (immich)"},
		{catalog.Service{Source: catalog.SourceCompose, Project: "jellyfin", Name: "Jellyfin"}, "Jellyfin"},
		{catalog.Service{Source: catalog.SourceSystemd, Name: "restic-backup.timer"}, "restic-backup.timer"},
	} {
		if got := alertName(tt.svc); got != tt.want {
			t.Errorf("alertName(%+v) = %q", tt.svc, got)
		}
	}
}

// fakeSender guarda lo mandado y falla con lo que le pongan en errs, en
// orden, antes de andar.
type fakeSender struct {
	mu   sync.Mutex
	errs []error
	sent []string // "chat|texto"
	try  int
}

func (f *fakeSender) Send(ctx context.Context, chat, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.try++
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	f.sent = append(f.sent, chat+"|"+text)
	return nil
}

type fakeAlertPrefs struct {
	alerts map[string]bool
	names  map[string]string
}

func (f fakeAlertPrefs) Alerts(context.Context) (map[string]bool, error)  { return f.alerts, nil }
func (f fakeAlertPrefs) Names(context.Context) (map[string]string, error) { return f.names, nil }

func testAlerter(send sender, prefs alertPrefs) (*alerter, *[]time.Duration) {
	a := newAlerter(send, []string{"111", "-222"}, "https://m.example.org/", prefs, time.UTC, slog.New(slog.DiscardHandler))
	var waits []time.Duration
	a.wait = func(ctx context.Context, d time.Duration) bool { waits = append(waits, d); return ctx.Err() == nil }
	return a, &waits
}

func TestAlerterDeliver(t *testing.T) {
	ctx := context.Background()
	svc := catalog.Service{ID: "web/app", Source: catalog.SourceCompose, Project: "web", Name: "app", Alert: true}
	down := store.StateEvent{Service: svc.ID, At: time.Unix(0, 0), From: "up", To: "down", Reason: "app detenido"}

	t.Run("a cada chat, con el nombre de la interfaz y el link", func(t *testing.T) {
		f := &fakeSender{}
		a, _ := testAlerter(f, fakeAlertPrefs{names: map[string]string{"service:web/app": "La App"}})
		a.deliver(ctx, alertNote{svc: svc, ev: down})
		if len(f.sent) != 2 || !strings.HasPrefix(f.sent[0], "111|🔴 <b>La App (web)</b>") || !strings.HasPrefix(f.sent[1], "-222|") {
			t.Fatalf("mandado: %q", f.sent)
		}
		if !strings.Contains(f.sent[0], `href="https://m.example.org/services/web/app"`) {
			t.Errorf("sin el link: %q", f.sent[0])
		}
	})

	t.Run("apagado desde la interfaz", func(t *testing.T) {
		f := &fakeSender{}
		a, _ := testAlerter(f, fakeAlertPrefs{alerts: map[string]bool{"service:web/app": false}})
		a.deliver(ctx, alertNote{svc: svc, ev: down})
		if f.try != 0 {
			t.Errorf("mandó %d", f.try)
		}
	})

	t.Run("reintenta lo que puede pasar solo", func(t *testing.T) {
		f := &fakeSender{errs: []error{
			errors.New("dial tcp: no such host"),
			&telegram.Error{Code: 429, RetryAfter: time.Minute},
		}}
		a, waits := testAlerter(f, fakeAlertPrefs{})
		a.deliver(ctx, alertNote{svc: svc, ev: down})
		if len(f.sent) != 2 {
			t.Fatalf("mandado: %q", f.sent)
		}
		// 10 s la primera; la segunda, el minuto que pide Telegram (más
		// que los 30 s de la escala).
		if want := []time.Duration{10 * time.Second, time.Minute}; len(*waits) != 2 || (*waits)[0] != want[0] || (*waits)[1] != want[1] {
			t.Errorf("esperas %v, quiero %v", *waits, want)
		}
	})

	t.Run("no reintenta un chat que no existe, y sigue con el otro", func(t *testing.T) {
		f := &fakeSender{errs: []error{&telegram.Error{Code: 400, Description: "Bad Request: chat not found"}}}
		a, waits := testAlerter(f, fakeAlertPrefs{})
		a.deliver(ctx, alertNote{svc: svc, ev: down})
		if f.try != 2 || len(f.sent) != 1 || len(*waits) != 0 {
			t.Errorf("intentos %d, mandado %q, esperas %v", f.try, f.sent, *waits)
		}
	})

	t.Run("se da por vencido después de la escala", func(t *testing.T) {
		var errs []error
		for range 2 * (len(retryWaits) + 1) {
			errs = append(errs, &telegram.Error{Code: 502})
		}
		f := &fakeSender{errs: errs}
		a, waits := testAlerter(f, fakeAlertPrefs{})
		a.deliver(ctx, alertNote{svc: svc, ev: down})
		if f.try != 2*(len(retryWaits)+1) || len(f.sent) != 0 || len(*waits) != 2*len(retryWaits) {
			t.Errorf("intentos %d, mandado %q, esperas %d", f.try, f.sent, len(*waits))
		}
	})
}

// El monitor le pasa al que avisa la caída de un contenedor y su vuelta,
// y no lo que no se avisa (el primer "arriba").
func TestMonitorAvisa(t *testing.T) {
	st, _ := openStore(t)
	m, _ := newTestMonitor(t, st)
	f := &fakeSender{}
	a, _ := testAlerter(f, st)
	a.chats = []string{"1"}
	m.notify = a.Notify

	snap := func(state string) Snapshot {
		return Snapshot{
			Hosts: []HostStatus{{Name: "vm", OK: true}},
			Services: []catalog.Service{{
				ID: "web/app", Source: catalog.SourceCompose, Host: "vm", Project: "web", Name: "app", Alert: true,
				Containers: []catalog.Container{{ID: "c1", Name: "app", State: state, ExitCode: 137, Host: "vm"}},
			}},
		}
	}
	m.Update(snap("running"))
	m.Update(snap("exited"))
	m.Update(snap("running"))
	if len(a.queue) != 2 {
		t.Fatalf("%d avisos en la cola, quiero 2", len(a.queue))
	}
	ctx := context.Background()
	a.deliver(ctx, <-a.queue)
	a.deliver(ctx, <-a.queue)
	if len(f.sent) != 2 || !strings.Contains(f.sent[0], "se cayó") || !strings.Contains(f.sent[0], "exit 137") || !strings.Contains(f.sent[1], "volvió a funcionar") {
		t.Errorf("mandado: %q", f.sent)
	}
}
