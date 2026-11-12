package icons

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

const testSelfhst = `[
  {"Name": "AIOStreams", "Reference": "aiostreams", "SVG": "Yes", "PNG": "Yes", "WebP": "Yes", "Light": "Yes", "Dark": "Yes"},
  {"Name": "NetBird", "Reference": "netbird", "SVG": "Yes", "PNG": "Yes", "WebP": "Yes"},
  {"Name": "Immich", "Reference": "immich", "SVG": "Yes", "PNG": "Yes", "WebP": "Yes"},
  {"Name": "Solo PNG", "Reference": "solo-png", "SVG": "No", "PNG": "Yes", "WebP": "Yes"}
]`

const testMeta = `{
  "immich": {"base": "svg", "aliases": []},
  "syncthing": {"base": "svg", "aliases": [], "colors": {"dark": "syncthing-dark", "light": "syncthing"}},
  "frigate": {"base": "svg", "aliases": ["NVR"], "colors": {"dark": "frigate", "light": "frigate-light"}},
  "jdownloader2": {"base": "svg", "aliases": []},
  "adguard-home": {"base": "svg", "aliases": ["AdGuard Home"]},
  "restic": {"base": "png", "aliases": []},
  "redis": {"base": "svg", "aliases": []},
  "ollama": {"base": "svg", "aliases": [], "colors": {"light": "ollama", "dark": "ollama-dark"}},
  "bad/name": {"base": "svg"}
}`

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	r := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Sin red: medir variantes no tiene que salir a Internet.
	r.upstream = map[string][]string{"di": {"http://127.0.0.1:1"}, "sh": {"http://127.0.0.1:1"}, "mdi": {"http://127.0.0.1:1"}, "si": {"http://127.0.0.1:1"}}
	if err := r.setIndex(r.col("di"), []byte(testMeta), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.setIndex(r.col("sh"), []byte(testSelfhst), time.Now()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveHints(t *testing.T) {
	r := testResolver(t)
	cases := []struct {
		hints []string
		want  Ref
		ok    bool
	}{
		{[]string{"Immich"}, Ref{Src: "/icons/di/immich.svg"}, true},
		{[]string{"JDownloader 2"}, Ref{Src: "/icons/di/jdownloader2.svg"}, true},
		{[]string{"AdGuardHome.service"}, Ref{Src: "/icons/di/adguard-home.svg"}, true},
		{[]string{"immich-server"}, Ref{Src: "/icons/di/immich.svg"}, true},
		{[]string{"syncthing"}, Ref{Src: "/icons/di/syncthing-dark.svg", SrcDark: "/icons/di/syncthing.svg"}, true},
		{[]string{"ollama"}, Ref{Src: "/icons/di/ollama-dark.svg", SrcDark: "/icons/di/ollama.svg"}, true},
		{[]string{"restic-backup.timer"}, Ref{Src: "/icons/di/restic.png"}, true},
		{[]string{"dns"}, Ref{}, false},
		{[]string{"nada-que-ver", "frigate"}, Ref{Src: "/icons/di/frigate.svg", SrcDark: "/icons/di/frigate-light.svg"}, true},
		// Los alias no cuentan para adivinar: son genéricos ("NVR").
		{[]string{"nvr"}, Ref{}, false},
		{nil, Ref{}, false},
	}
	for _, c := range cases {
		got, ok := r.Resolve("", c.hints...)
		if ok != c.ok || got != c.want {
			t.Errorf("Resolve(%q) = %+v, %v; quería %+v, %v", c.hints, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveSpec(t *testing.T) {
	r := testResolver(t)
	cases := []struct {
		spec string
		want Ref
		ok   bool
	}{
		{"immich", Ref{Src: "/icons/di/immich.svg"}, true},
		{"immich.png", Ref{Src: "/icons/di/immich.png"}, true},
		{"restic.png", Ref{Src: "/icons/di/restic.png"}, true},
		{"AdGuard Home", Ref{Src: "/icons/di/adguard-home.svg"}, true},
		{"nvr", Ref{Src: "/icons/di/frigate.svg", SrcDark: "/icons/di/frigate-light.svg"}, true},
		{"mdi-dumbbell", Ref{Src: "/icons/mdi/dumbbell.svg", Mono: true}, true},
		{"si-github", Ref{Src: "/icons/si/github.svg", Mono: true}, true},
		{"https://example.org/x.png", Ref{Src: "https://example.org/x.png"}, true},
		{"/static/x.svg", Ref{Src: "/static/x.svg"}, true},
		{"//evil.example/x.svg", Ref{}, false},
		{"mdi-../../x", Ref{}, false},
		{"shield", Ref{}, false}, // Material Symbol: lo resuelve el hub
	}
	for _, c := range cases {
		// El declarado manda sobre las pistas.
		got, ok := r.Resolve(c.spec, "redis")
		if ok != c.ok || got != c.want {
			t.Errorf("Resolve(%q) = %+v, %v; quería %+v, %v", c.spec, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveNil(t *testing.T) {
	var r *Resolver
	if _, ok := r.Resolve("immich"); ok {
		t.Error("un Resolver nil no resuelve nada")
	}
}

func TestImageHints(t *testing.T) {
	cases := map[string][]string{
		"ghcr.io/immich-app/immich-server:release":        {"immich-server", "immich-app"},
		"lscr.io/linuxserver/syncthing:latest":            {"syncthing"},
		"jlesage/jdownloader-2@sha256:abc":                {"jdownloader-2"},
		"redis:7":                                         {"redis"},
		"localhost:5000/team/app:1":                       {"app", "team"},
		"ghcr.io/blakeblackshear/frigate:0.14.1-tensorrt": {"frigate", "blakeblackshear"},
		"docker.io/library/postgres:16":                   {"postgres"},
	}
	for in, want := range cases {
		if got := ImageHints(in); !slices.Equal(got, want) {
			t.Errorf("ImageHints(%q) = %q; quería %q", in, got, want)
		}
	}
}

func TestServe(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/di/svg/immich.svg", "/mdi/dumbbell.svg":
			io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg"/>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	r := testResolver(t)
	r.upstream = map[string][]string{"di": {up.URL + "/cdn-di", up.URL + "/di"}, "sh": {up.URL + "/sh"}, "mdi": {up.URL + "/mdi"}, "si": {up.URL + "/si"}}

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	for range 2 {
		rec := get("/di/immich.svg")
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || rec.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("immich: %d %v", rec.Code, rec.Header())
		}
	}
	// El primer origen (el CDN) responde 404: se baja del segundo. La
	// segunda vez sale del disco.
	if hits.Load() != 2 {
		t.Errorf("tenían que ser 2 pedidos (CDN y GitHub): %d", hits.Load())
	}
	if _, err := os.Stat(filepath.Join(r.dir, "di", "immich.svg")); err != nil {
		t.Error(err)
	}
	if rec := get("/mdi/dumbbell.svg"); rec.Code != 200 {
		t.Errorf("mdi: %d", rec.Code)
	}
	// Variantes de uno que está en el índice: se piden.
	if rec := get("/di/syncthing-dark.svg"); rec.Code != 404 || hits.Load() != 5 {
		t.Errorf("variante: %d, %d pedidos", rec.Code, hits.Load())
	}
	// Una falla no se vuelve a pedir enseguida.
	get("/di/syncthing-dark.svg")
	if hits.Load() != 5 {
		t.Errorf("reintentó enseguida: %d pedidos", hits.Load())
	}
	// Lo que no está en el índice, lo que no tiene formato válido y las
	// colecciones que no existen no salen a la red.
	for _, p := range []string{"/di/nada.svg", "/sh/nada.svg", "/di/immich.exe", "/x/immich.svg", "/di/..%2Fx.svg", "/mdi/x.png"} {
		if rec := get(p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if hits.Load() != 5 {
		t.Errorf("salió a la red por algo inválido: %d pedidos", hits.Load())
	}
}

func TestRunFetchesAndCachesIndex(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/di/metadata.json":
			io.WriteString(w, testMeta)
		case "/sh/index.json":
			io.WriteString(w, testSelfhst)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	dir := t.TempDir()
	r := New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.upstream["di"] = []string{up.URL + "/di"}
	r.upstream["sh"] = []string{up.URL + "/sh"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for (r.loadedAt(r.col("di")).IsZero() || r.loadedAt(r.col("sh")).IsZero()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if _, ok := r.Resolve("", "immich"); !ok {
		t.Fatal("no cargó el índice")
	}
	// Otro Resolver sobre la misma carpeta lo lee del disco, sin red.
	up.Close()
	r2 := New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	raw, mod, err := r2.readIndex(r2.col("di"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r2.setIndex(r2.col("di"), raw, mod); err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Resolve("", "immich"); !ok {
		t.Fatal("el índice en disco no sirve")
	}
}

// Si las variantes están en disco, la más clara va sobre fondo oscuro
// aunque el nombre diga otra cosa (ollama-dark es la blanca).
func TestVariantByLuminance(t *testing.T) {
	r := testResolver(t)
	dir := filepath.Join(r.dir, "di")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "ollama.svg"), []byte(`<svg><path fill="black"/><path fill="#000"/></svg>`), 0o644)
	os.WriteFile(filepath.Join(dir, "ollama-dark.svg"), []byte(`<svg><path fill="white"/><path style="fill:#ffffff"/></svg>`), 0o644)
	got, _ := r.Resolve("", "ollama")
	if want := (Ref{Src: "/icons/di/ollama.svg", SrcDark: "/icons/di/ollama-dark.svg"}); got != want {
		t.Errorf("ollama = %+v; quería %+v", got, want)
	}
}

func TestSVGLuminance(t *testing.T) {
	cases := map[string]float64{
		`<svg/>`:              0,
		`<path fill="#fff"/>`: 1,
		`<path fill="white"/><path fill="black"/>`: 0.5,
		`<stop stop-color="#000000"/>`:             0,
	}
	for in, want := range cases {
		if got := svgLuminance([]byte(in)); got < want-0.01 || got > want+0.01 {
			t.Errorf("svgLuminance(%s) = %v; quería %v", in, got, want)
		}
	}
}

// selfh.st completa lo que dashboard-icons no tiene; si los dos tienen un
// logo, gana dashboard-icons.
func TestSelfhst(t *testing.T) {
	r := testResolver(t)
	cases := []struct {
		spec  string
		hints []string
		want  Ref
	}{
		{"", []string{"aiostreams"}, Ref{Src: "/icons/sh/aiostreams.svg"}},
		{"", []string{"immich"}, Ref{Src: "/icons/di/immich.svg"}},
		{"sh-immich", nil, Ref{Src: "/icons/sh/immich.svg"}},
		{"netbird", nil, Ref{Src: "/icons/sh/netbird.svg"}},
		{"NetBird", nil, Ref{Src: "/icons/sh/netbird.svg"}},
		{"sh-solo-png", nil, Ref{Src: "/icons/sh/solo-png.png"}},
	}
	for _, c := range cases {
		if got, ok := r.Resolve(c.spec, c.hints...); !ok || got != c.want {
			t.Errorf("Resolve(%q, %q) = %+v, %v; quería %+v", c.spec, c.hints, got, ok, c.want)
		}
	}
}

func TestSearch(t *testing.T) {
	r := testResolver(t)
	var got []string
	for _, h := range r.Search("Imm", 10) {
		got = append(got, h.Spec)
	}
	// immich está en las dos: una sola vez, la de dashboard-icons.
	if want := []string{"immich"}; !slices.Equal(got, want) {
		t.Errorf("Search(Imm) = %q; quería %q", got, want)
	}
	got = nil
	for _, h := range r.Search("bird", 10) {
		got = append(got, h.Spec)
	}
	if want := []string{"sh-netbird"}; !slices.Equal(got, want) {
		t.Errorf("Search(bird) = %q; quería %q", got, want)
	}
	if len(r.Search("o", 2)) != 2 || r.Search("", 5) != nil {
		t.Error("Search no respeta el límite o busca vacío")
	}
}
