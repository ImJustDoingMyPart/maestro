package hub

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// fakeStacks responde lo que se le cargue. Las acciones mandan las líneas
// de lines y terminan con exit; si gate no es nil, esperan a que se cierre
// antes del final.
type fakeStacks struct {
	mu      sync.Mutex
	files   agent.StackFiles
	err     error // para Files, Check y Write
	diff    string
	written string
	lines   []string
	exit    int
	gate    chan struct{}
	actErr  error
}

func (f *fakeStacks) StackFiles(_ context.Context, host, project string) (agent.StackFiles, error) {
	if host != "vm" {
		return agent.StackFiles{}, errNoAgent
	}
	return f.files, f.err
}

func (f *fakeStacks) CheckStackFile(_ context.Context, _, _, _, content string) (agent.CheckResult, error) {
	return agent.CheckResult{Diff: f.diff}, f.err
}

func (f *fakeStacks) WriteStackFile(_ context.Context, _, _, _, content, hash string) (agent.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agent.WriteResult{}, f.err
	}
	f.written = content
	return agent.WriteResult{Hash: "sha256:nuevo", Diff: f.diff, Commit: "abc1234"}, nil
}

func (f *fakeStacks) StackAction(_ context.Context, _, _, action string) (iter.Seq2[agent.ActionEvent, error], error) {
	if f.actErr != nil {
		return nil, f.actErr
	}
	return func(yield func(agent.ActionEvent, error) bool) {
		for _, l := range f.lines {
			if !yield(agent.ActionEvent{TS: time.Now(), Stream: "stderr", Line: l}, nil) {
				return
			}
		}
		if f.gate != nil {
			<-f.gate
		}
		code := f.exit
		yield(agent.ActionEvent{Exit: &code}, nil)
	}, nil
}

func stackSnap() Snapshot {
	return Snapshot{Services: []catalog.Service{
		{ID: "media/jellyfin", Source: catalog.SourceCompose, Name: "Jellyfin", Host: "vm", Project: "media"},
		{ID: "media/radarr", Source: catalog.SourceCompose, Name: "Radarr", Host: "vm", Project: "media"},
	}}
}

func postJSON(t *testing.T, c *http.Client, srv *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	var r *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	} else {
		r = strings.NewReader("")
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", srv.URL)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("POST %s: respuesta no JSON (%d): %v", path, res.StatusCode, err)
	}
	return res.StatusCode, out
}

func TestPaginaDeStack(t *testing.T) {
	fs := &fakeStacks{files: agent.StackFiles{Project: "media", Dir: "/srv/media", Files: []agent.StackFile{
		{Name: agent.FileCompose, Path: "/srv/media/compose.yml", Exists: true, Content: "services:\n  x: {image: \"<script>\"}\n", Hash: "sha256:1"},
		{Name: agent.FileEnv, Path: "/srv/media/.env"},
	}}}
	srv := testServerWith(t, stackSnap(), nil, fs)
	c := loggedIn(t, srv)

	res, err := c.Get(srv.URL + "/stacks/vm/media?svc=media/jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	b := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	for _, want := range []string{
		"&lt;script&gt;",                  // el contenido va escapado
		`data-hash="sha256:1"`,            // con el hash para guardar
		"tiene 2 servicios",               // avisa que actúa sobre todo el stack
		`href="/services/media/jellyfin"`, // pestaña Actividad del servicio de origen
		"compose.yml",                     // nombre del archivo en la pestaña
		"/static/gen/compose.js",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q en la página", want)
		}
	}
	if strings.Contains(b, "<script>\"") {
		t.Error("el contenido del compose salió sin escapar")
	}

	for path, want := range map[string]int{
		"/stacks/otro/media": http.StatusNotFound, // host sin agent
		"/stacks/vm/Mayus":   http.StatusBadRequest,
	} {
		res, _ := c.Get(srv.URL + path)
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s = %d, quería %d", path, res.StatusCode, want)
		}
	}
	fs.err = agent.ErrStackNotFound
	res, _ = c.Get(srv.URL + "/stacks/vm/media")
	if b := body(t, res); res.StatusCode != http.StatusNotFound || !strings.Contains(b, "no encuentra el stack") {
		t.Errorf("stack inexistente: %d", res.StatusCode)
	}
}

func TestGuardarArchivo(t *testing.T) {
	fs := &fakeStacks{diff: "+x"}
	srv := testServerWith(t, stackSnap(), nil, fs)
	c := loggedIn(t, srv)

	code, out := postJSON(t, c, srv, "/stacks/vm/media/files/compose/check", map[string]string{"content": "x"})
	if code != http.StatusOK || out["diff"] != "+x" {
		t.Errorf("check = %d %v", code, out)
	}
	code, out = postJSON(t, c, srv, "/stacks/vm/media/files/compose", map[string]string{"content": "nuevo", "hash": "sha256:1"})
	if code != http.StatusOK || out["commit"] != "abc1234" || fs.written != "nuevo" {
		t.Errorf("guardar = %d %v (escribió %q)", code, out, fs.written)
	}

	tests := []struct {
		err  error
		code int
		want string
	}{
		{&agent.InvalidError{Output: "línea 3: mal"}, http.StatusUnprocessableEntity, "línea 3: mal"},
		{agent.ErrConflict, http.StatusConflict, "Recargala"},
		{agent.ErrBusy, http.StatusConflict, "otra operación"},
		{errors.New("connection refused"), http.StatusBadGateway, "connection refused"},
	}
	for _, tt := range tests {
		fs.err = tt.err
		code, out := postJSON(t, c, srv, "/stacks/vm/media/files/env", map[string]string{"content": "x"})
		got, _ := json.Marshal(out)
		if code != tt.code || !strings.Contains(string(got), tt.want) {
			t.Errorf("%v: %d %s, quería %d con %q", tt.err, code, got, tt.code, tt.want)
		}
	}

	fs.err = nil
	if code, _ := postJSON(t, c, srv, "/stacks/vm/media/files/passwd", map[string]string{"content": "x"}); code != http.StatusNotFound {
		t.Errorf("archivo que no se edita = %d", code)
	}

	// Sin Origin del mismo sitio, CrossOriginProtection lo rechaza.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/stacks/vm/media/files/compose", strings.NewReader(`{"content":"x"}`))
	req.Header.Set("Origin", "https://otro.example")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST de otro origen = %d, quería 403", res.StatusCode)
	}
}

func TestAccion(t *testing.T) {
	gate := make(chan struct{})
	fs := &fakeStacks{lines: []string{"Container media-jellyfin-1 Recreate", "Container media-jellyfin-1 Started"}, exit: 0, gate: gate}
	srv := testServerWith(t, stackSnap(), nil, fs)
	c := loggedIn(t, srv)

	code, out := postJSON(t, c, srv, "/stacks/vm/media/actions/up", nil)
	runURL, _ := out["run"].(string)
	if code != http.StatusOK || !strings.HasPrefix(runURL, "/runs/") {
		t.Fatalf("up = %d %v", code, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := openStream(t, ctx, c, srv.URL+runURL, "")
	for i, want := range fs.lines {
		ev := s.next(t)
		if !strings.Contains(ev.Data, want) || ev.ID != strconv.Itoa(i+1) {
			t.Errorf("evento %d = %+v", i, ev)
		}
	}
	// Mientras sigue, la página del stack la muestra.
	fs.files = agent.StackFiles{Files: []agent.StackFile{{Name: agent.FileCompose, Path: "/x/compose.yml"}}}
	res, _ := c.Get(srv.URL + "/stacks/vm/media")
	if b := body(t, res); !strings.Contains(b, `data-run="`+runURL+`"`) {
		t.Error("la página no muestra la acción en curso")
	}
	cancel() // el navegador se va; la acción sigue

	close(gate)
	// Reconectar con Last-Event-ID: no repite y trae el final.
	s = openStream(t, context.Background(), c, srv.URL+runURL, "1")
	if ev := s.next(t); !strings.Contains(ev.Data, "Started") {
		t.Errorf("después de reconectar = %+v", ev)
	}
	ev := s.next(t)
	var end struct {
		Text string
		OK   bool
	}
	_ = json.Unmarshal([]byte(ev.Data), &end)
	if ev.Event != "end" || !end.OK || !strings.Contains(end.Text, "up terminó bien") {
		t.Errorf("fin = %+v", ev)
	}
	s.closed(t)

	fs.actErr = agent.ErrBusy
	if code, _ := postJSON(t, c, srv, "/stacks/vm/media/actions/pull", nil); code != http.StatusConflict {
		t.Errorf("ocupado = %d", code)
	}
	if code, _ := postJSON(t, c, srv, "/stacks/vm/media/actions/rm", nil); code != http.StatusNotFound {
		t.Errorf("acción desconocida = %d", code)
	}
	res, _ = c.Get(srv.URL + "/runs/no-existe")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("run inexistente = %d", res.StatusCode)
	}
}

func TestRunStore(t *testing.T) {
	s := newRunStore()
	t0 := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return t0 }

	r := newRun("vm", "media", "up", t0)
	s.add(r)
	if got, ok := s.latest("vm", "media"); !ok || got != r {
		t.Fatal("latest no devuelve la acción en curso")
	}
	r.finish(1, "", t0)
	if text, ok := r.summary(); ok || !strings.Contains(text, "código 1") {
		t.Errorf("summary = %q, %v", text, ok)
	}

	s.now = func() time.Time { return t0.Add(runRecent + time.Second) }
	if _, ok := s.latest("vm", "media"); ok {
		t.Error("una acción vieja sigue apareciendo en la página")
	}
	if _, ok := s.get(r.ID); !ok {
		t.Error("se borró antes de runKeep")
	}

	s.now = func() time.Time { return t0.Add(runKeep + time.Minute) }
	s.add(newRun("vm", "otro", "pull", s.now()))
	if _, ok := s.get(r.ID); ok {
		t.Error("no se borró después de runKeep")
	}

	// El tope de renglones.
	big := newRun("vm", "x", "pull", t0)
	for range maxRunLines + 5 {
		big.append(runLine{Line: "x"})
	}
	big.finish(0, "", t0)
	if text, _ := big.summary(); !strings.Contains(text, "Se omitieron 5") {
		t.Errorf("summary = %q", text)
	}
}
