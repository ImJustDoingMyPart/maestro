package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
	"github.com/ImJustDoingMyPart/maestro/internal/telegram"
)

// Avisos a Telegram (etapa 8). Avisa lo mismo que Uptime Kuma: cuando un
// servicio pasa a Caído y cuando sale de Caído. Degradado (una falla, un
// reinicio) no avisa: haría ruido. Sin datos (Unknown) no es un cambio de
// estado, así que un agent que no responde tampoco avisa por sus servicios.
//
// Quién avisa: el alert de maestro.yaml; si no lo declara, el interruptor
// de la interfaz ("Avisar por Telegram" en el menú de la tarjeta); si
// tampoco, maestro.alert o el valor por defecto (con checks, o una unidad
// de systemd o un invitado de Proxmox).

// worthAlert dice si un cambio de estado se avisa: la entrada a Caído y la
// salida de Caído.
func worthAlert(from, to status.State) bool {
	return (to == status.Down) != (from == status.Down)
}

// alertOn dice si un servicio avisa, con lo puesto desde la interfaz (ui,
// por clave de servicio).
func alertOn(svc catalog.Service, ui map[string]bool) bool {
	if svc.AlertFromFile {
		return svc.Alert
	}
	if v, ok := ui[serviceKey(svc.ID)]; ok {
		return v
	}
	return svc.Alert
}

// alertName es cómo se nombra un servicio en el aviso: su nombre y, si es
// parte de un stack con otro nombre, el proyecto ("immich-server (immich)").
func alertName(svc catalog.Service) string {
	if svc.Source == catalog.SourceCompose && svc.Project != "" && !strings.EqualFold(svc.Name, svc.Project) {
		return svc.Name + " (" + svc.Project + ")"
	}
	return svc.Name
}

// maxReason es el largo máximo del motivo en un aviso, en caracteres: un
// error de un check puede traer un cuerpo HTML entero.
const maxReason = 300

// alertText arma el aviso en el HTML de Telegram, con la forma de la
// plantilla que tenía Uptime Kuma:
//
//	🔴 <b>Immich</b> dejó de responder
//	🕗 14:32 · 💬 check http: timeout
//	🔗 Ver en Maestro
//
// link es la página del servicio en Maestro; vacío si no hay public_url.
func alertText(name string, e store.StateEvent, loc *time.Location, link string) string {
	var b strings.Builder
	n := "<b>" + telegram.Escape(name) + "</b>"
	reason := e.Reason
	switch status.State(e.To) {
	case status.Down:
		verb := "se cayó"
		if strings.HasPrefix(reason, "check ") || strings.HasPrefix(reason, "push:") {
			verb = "dejó de responder"
		}
		fmt.Fprintf(&b, "🔴 %s %s", n, verb)
	case status.Up:
		fmt.Fprintf(&b, "✅ %s volvió a funcionar", n)
		reason = "" // "arriba" o "volvió a responder" no dicen nada más
	case status.Degraded:
		fmt.Fprintf(&b, "🟡 %s volvió, con problemas", n)
	case status.Stopped:
		fmt.Fprintf(&b, "⏸️ %s quedó detenido", n)
	default:
		fmt.Fprintf(&b, "❔ %s: %s", n, telegram.Escape(e.To))
	}
	fmt.Fprintf(&b, "\n🕗 %s", e.At.In(loc).Format("15:04"))
	if reason != "" {
		if utf8.RuneCountInString(reason) > maxReason {
			reason = string([]rune(reason)[:maxReason-1]) + "…"
		}
		fmt.Fprintf(&b, " · 💬 %s", telegram.Escape(reason))
	}
	if link != "" {
		fmt.Fprintf(&b, "\n🔗 <a href=\"%s\">Ver en Maestro</a>", telegram.Escape(link))
	} else {
		b.WriteString("\n<i>Maestro</i>")
	}
	return b.String()
}

// testText es el mensaje de "maestro telegram-test".
const testText = "⚠️ <b>Prueba</b> desde Maestro. Si esto se ve en negrita, el formato quedó bien."

// sender manda un mensaje a un chat. Lo cumple *telegram.Client.
type sender interface {
	Send(ctx context.Context, chat, text string) error
}

// alertPrefs es lo que el que avisa lee de la base. Lo cumple *store.Store.
type alertPrefs interface {
	Alerts(ctx context.Context) (map[string]bool, error)
	Names(ctx context.Context) (map[string]string, error)
}

// alertNote es un cambio de estado que puede avisarse, con el servicio tal
// como era en ese momento.
type alertNote struct {
	svc catalog.Service
	ev  store.StateEvent
}

// alertQueue es cuántos cambios pueden esperar a salir. Uno que no entra
// se pierde (y se registra): el monitor nunca espera a Telegram.
const alertQueue = 128

// retryWaits son las esperas entre intentos cuando Telegram o la red
// fallan: casi una hora en total. Después el aviso se da por perdido.
var retryWaits = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute}

// alerter manda los avisos. Notify encola; Run los manda de a uno, en
// orden, con reintentos.
type alerter struct {
	send      sender
	chats     []string
	publicURL string // sin "/" al final; vacío: sin link
	prefs     alertPrefs
	loc       *time.Location
	logger    *slog.Logger
	queue     chan alertNote
	// wait espera d o hasta que se cancele ctx (false). Los tests la
	// cambian para no esperar de verdad.
	wait func(ctx context.Context, d time.Duration) bool
}

func newAlerter(send sender, chats []string, publicURL string, prefs alertPrefs, loc *time.Location, logger *slog.Logger) *alerter {
	return &alerter{
		send: send, chats: chats, publicURL: strings.TrimRight(publicURL, "/"),
		prefs: prefs, loc: loc, logger: logger,
		queue: make(chan alertNote, alertQueue),
		wait:  sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Notify recibe un cambio de estado del monitor. No bloquea: lo llama el
// monitor con su lock tomado.
func (a *alerter) Notify(svc catalog.Service, ev store.StateEvent) {
	if !worthAlert(status.State(ev.From), status.State(ev.To)) {
		return
	}
	select {
	case a.queue <- alertNote{svc: svc, ev: ev}:
	default:
		a.logger.Error("aviso perdido: la cola está llena", "service", svc.ID, "to", ev.To)
	}
}

// Run manda los avisos encolados hasta que se cancele ctx.
func (a *alerter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-a.queue:
			a.deliver(ctx, n)
		}
	}
}

// deliver decide si el cambio avisa (con lo de la interfaz de ahora) y lo
// manda a cada chat.
func (a *alerter) deliver(ctx context.Context, n alertNote) {
	key := serviceKey(n.svc.ID)
	ui, err := a.prefs.Alerts(ctx)
	if err != nil {
		a.logger.Error("leer avisos de la interfaz", "err", err)
	}
	if !alertOn(n.svc, ui) {
		return
	}
	if !n.svc.NameFromFile {
		if names, err := a.prefs.Names(ctx); err != nil {
			a.logger.Error("leer nombres", "err", err)
		} else if name := names[key]; name != "" {
			n.svc.Name = name
		}
	}
	link := ""
	if a.publicURL != "" {
		link = a.publicURL + servicePath(n.svc.ID)
	}
	text := alertText(alertName(n.svc), n.ev, a.loc, link)
	for _, chat := range a.chats {
		if err := a.sendRetrying(ctx, chat, text); err != nil {
			// Sin el chat en el log: es parte de la configuración secreta.
			a.logger.Error("aviso a Telegram perdido", "service", n.svc.ID, "to", n.ev.To, "err", err)
			continue
		}
		a.logger.Info("aviso a Telegram", "service", n.svc.ID, "from", n.ev.From, "to", n.ev.To)
	}
}

// sendRetrying manda text a chat y reintenta lo que puede pasar solo (la
// red, Telegram caído, demasiados mensajes) con las esperas de retryWaits.
func (a *alerter) sendRetrying(ctx context.Context, chat, text string) error {
	for i := 0; ; i++ {
		err := a.send.Send(ctx, chat, text)
		if err == nil {
			return nil
		}
		retry, after := telegram.Retryable(err)
		if !retry || i == len(retryWaits) {
			return err
		}
		d := max(retryWaits[i], after)
		a.logger.Warn("Telegram no tomó el aviso; se reintenta", "en", d, "err", err)
		if !a.wait(ctx, d) {
			return ctx.Err()
		}
	}
}

// TelegramTest manda un mensaje de prueba a cada chat de maestro.yaml
// ("maestro telegram-test"). Dice a qué chat llegó (por número de orden:
// los chats son secretos) y devuelve los errores juntos.
func TelegramTest(ctx context.Context, configPath string, out io.Writer) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if cfg.Telegram == nil {
		return errors.New("maestro.yaml no tiene telegram:")
	}
	c := telegram.New(cfg.Telegram.Token, "")
	var errs []error
	for i, chat := range cfg.Telegram.Chats() {
		if err := c.Send(ctx, chat, testText); err != nil {
			errs = append(errs, fmt.Errorf("chat %d: %w", i+1, err))
			continue
		}
		fmt.Fprintf(out, "chat %d: mandado\n", i+1)
	}
	return errors.Join(errs...)
}

// setAlert atiende POST /alert: con alert=1 prende los avisos de "key" y
// con 0 los apaga. La clave es la de un servicio ("service:…", "found:…")
// o la de un stack ("stack:<host>/<proyecto>"), que cambia todos sus
// servicios salvo los que fija el archivo. Si el archivo fija el servicio
// (o todos los del stack), 409.
func (s *server) setAlert(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	v := r.PostFormValue("alert") == "1"
	snap := s.poller.Snapshot()
	var targets []catalog.Service
	kind, rest, _ := strings.Cut(key, ":")
	switch kind {
	case "stack":
		host, project, _ := strings.Cut(rest, "/")
		for _, svc := range snap.Services {
			if svc.Source == catalog.SourceCompose && svc.Host == host && svc.Project == project {
				targets = append(targets, svc)
			}
		}
	case "service":
		if svc, found := findService(snap, rest); found {
			targets = append(targets, svc)
		}
	case "found":
		for _, svc := range withShown(snap, s.loadPrefs(r.Context()).Shown) {
			if svc.ID == key {
				targets = append(targets, svc)
			}
		}
	}
	if len(targets) == 0 {
		http.Error(w, "no existe ese servicio", http.StatusNotFound)
		return
	}
	changed := 0
	now := time.Now()
	for _, svc := range targets {
		if svc.AlertFromFile {
			continue
		}
		if err := s.prefs.SetAlert(r.Context(), serviceKey(svc.ID), &v, now); err != nil {
			s.logger.Error("avisos", "key", key, "err", err)
			http.Error(w, "no se pudo guardar", http.StatusInternalServerError)
			return
		}
		changed++
	}
	if changed == 0 {
		http.Error(w, "alert viene de maestro.yaml", http.StatusConflict)
		return
	}
	s.gridOrHome(w, r)
}
