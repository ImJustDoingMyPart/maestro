// Package icons da a cada servicio el logo de su aplicación.
//
// Los logos salen de dashboard-icons (github.com/homarr-labs/dashboard-icons,
// los mismos que usa Homepage), y para íconos monocromos de Material Design
// Icons (prefijo "mdi-") y Simple Icons ("si-"). El navegador nunca habla
// con un tercero: pide /icons/<colección>/<archivo> al hub, que lo baja de
// jsDelivr la primera vez y lo guarda en disco al lado de la base. Después
// de eso anda sin Internet.
//
// Un servicio sin ícono declarado igual lo consigue: Resolve prueba con su
// nombre, su proyecto de compose, su imagen o su unidad contra el índice de
// dashboard-icons (metadata.json, que también se guarda en disco y se
// renueva una vez por semana).
package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Orígenes. Los paquetes de npm van con versión fija; dashboard-icons no
// publica versiones y se usa su rama principal, como hace Homepage.
const (
	diBase   = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons"
	mdiBase  = "https://cdn.jsdelivr.net/npm/@mdi/svg@7.4.47/svg"
	siBase   = "https://cdn.jsdelivr.net/npm/simple-icons@16.37.0/icons"
	maxFile  = 2 << 20  // un logo pesa unos KB; el PNG más grande, cientos
	maxIndex = 16 << 20 // metadata.json ronda 1,3 MB
)

const (
	indexFile    = "dashboard-icons.json"
	indexMaxAge  = 7 * 24 * time.Hour
	retryAfter   = time.Hour // tras una falla, ni el índice ni un archivo se piden de nuevo antes
	fetchTimeout = 20 * time.Second
)

// Ref es un ícono listo para la plantilla.
type Ref struct {
	Src string // para fondo claro (o el único)
	// SrcDark es la variante para fondo oscuro, si el logo la tiene.
	SrcDark string
	// Mono es un glifo de un color (negro): la interfaz lo invierte en el
	// tema oscuro.
	Mono bool
}

// entry es un logo del índice.
type entry struct {
	name   string
	format string // svg, png o webp
	// onLight y onDark son las variantes para fondo claro y oscuro ("" si
	// no tiene).
	onLight, onDark string
}

// Resolver resuelve y sirve los íconos. El cero no sirve: usar New.
type Resolver struct {
	dir    string
	client *http.Client
	logger *slog.Logger
	// upstream es el origen de cada colección (di, mdi, si); los tests lo
	// cambian por un servidor local.
	upstream map[string]string

	mu      sync.RWMutex
	byName  map[string]entry // nombre exacto ("adguard-home")
	byKey   map[string]entry // nombre normalizado ("adguardhome")
	byAlias map[string]entry // alias normalizado; solo para íconos declarados
	failed  map[string]time.Time
	loaded  time.Time // cuándo se bajó el índice que está en memoria

	lumaMu   sync.Mutex
	luma     map[string]float64 // luminancia media de un SVG en disco, por nombre
	fetching map[string]bool    // variantes que se están bajando para medirlas
}

// New crea un Resolver que guarda en dir. No toca la red: eso lo hace Run.
func New(dir string, logger *slog.Logger) *Resolver {
	return &Resolver{
		dir:      dir,
		client:   &http.Client{Timeout: fetchTimeout},
		logger:   logger,
		failed:   map[string]time.Time{},
		luma:     map[string]float64{},
		fetching: map[string]bool{},
		upstream: map[string]string{"di": diBase, "mdi": mdiBase, "si": siBase},
	}
}

// Run carga el índice (del disco si está, si no de la red) y lo renueva una
// vez por semana, hasta que se cancele ctx. Sin índice, Resolve solo
// resuelve los "mdi-", "si-" y las URLs.
func (r *Resolver) Run(ctx context.Context) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		r.logger.Warn("íconos: no se pudo crear la carpeta", "dir", r.dir, "err", err)
		return
	}
	if raw, mod, err := r.readIndex(); err == nil {
		if err := r.setIndex(raw, mod); err != nil {
			r.logger.Warn("íconos: índice en disco ilegible", "err", err)
		}
	}
	for {
		wait := time.Until(r.loadedAt().Add(indexMaxAge))
		if wait <= 0 {
			if err := r.fetchIndex(ctx); err != nil {
				r.logger.Warn("íconos: no se pudo bajar el índice de dashboard-icons", "err", err)
				wait = retryAfter
			} else {
				wait = indexMaxAge
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (r *Resolver) loadedAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded
}

func (r *Resolver) readIndex() ([]byte, time.Time, error) {
	p := filepath.Join(r.dir, indexFile)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, time.Time{}, err
	}
	raw, err := os.ReadFile(p)
	return raw, fi.ModTime(), err
}

func (r *Resolver) fetchIndex(ctx context.Context) error {
	raw, err := r.get(ctx, r.upstream["di"]+"/metadata.json", maxIndex)
	if err != nil {
		return err
	}
	if err := r.setIndex(raw, time.Now()); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir, indexFile), raw)
}

// setIndex lee metadata.json: {"<nombre>": {"base": "svg", "aliases": [...],
// "colors": {"dark": "<variante para fondo claro>", "light": "<para oscuro>"}}}.
func (r *Resolver) setIndex(raw []byte, at time.Time) error {
	var meta map[string]struct {
		Base    string   `json:"base"`
		Aliases []string `json:"aliases"`
		Colors  struct {
			Dark  string `json:"dark"`
			Light string `json:"light"`
		} `json:"colors"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if len(meta) == 0 {
		return errors.New("índice vacío")
	}
	byName := make(map[string]entry, len(meta))
	byKey := make(map[string]entry, len(meta))
	byAlias := map[string]entry{}
	for name, m := range meta {
		if !validName.MatchString(name) {
			continue
		}
		e := entry{name: name, format: m.Base}
		if e.format != "png" && e.format != "webp" {
			e.format = "svg"
		}
		if validName.MatchString(m.Colors.Dark) && validName.MatchString(m.Colors.Light) {
			e.onLight, e.onDark = variants2(m.Colors.Dark, m.Colors.Light)
		}
		byName[name] = e
		k := key(name)
		// Si dos nombres se normalizan igual, gana el que ya era la clave
		// ("jdownloader2" sobre un hipotético "jdownloader-2").
		if old, ok := byKey[k]; !ok || old.name != k && name == k {
			byKey[k] = e
		}
		for _, a := range m.Aliases {
			if ak := key(a); ak != "" {
				if _, ok := byAlias[ak]; !ok {
					byAlias[ak] = e
				}
			}
		}
	}
	r.mu.Lock()
	r.byName, r.byKey, r.byAlias, r.loaded = byName, byKey, byAlias, at
	r.mu.Unlock()
	return nil
}

// variants2 ordena las dos variantes de un logo: cuál va sobre fondo claro
// y cuál sobre oscuro. Las claves "dark" y "light" de metadata.json no
// significan lo mismo en todas las entradas (en frigate "light" es la
// blanca; en ollama, la negra), así que manda el nombre del archivo, como
// dice el README: "-light" es para fondo oscuro y "-dark" para fondo claro.
func variants2(dark, light string) (onLight, onDark string) {
	switch {
	case strings.HasSuffix(dark, "-light") || strings.HasSuffix(light, "-dark"):
		return light, dark
	default:
		return dark, light
	}
}

// validName es un nombre de archivo de ícono sin extensión.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// key normaliza un nombre para compararlo: minúsculas, solo letras y
// números. "AdGuard Home", "adguard-home" y "AdGuardHome" dan lo mismo.
func key(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// Resolve devuelve el ícono de un servicio. spec es el declarado (label
// maestro.icon u homepage.icon, o icon en maestro.yaml) y manda si se
// encuentra; hints son nombres para adivinarlo (el del servicio, su
// proyecto, su imagen), en orden de preferencia. ok es false si no hay
// logo: la interfaz pone el ícono genérico.
//
// Formatos de spec, los mismos que Homepage: "immich" o "immich.png"
// (dashboard-icons), "mdi-dumbbell", "si-github" y una URL. Un nombre que
// no está en dashboard-icons no resuelve: el hub lo prueba como Material
// Symbol.
func (r *Resolver) Resolve(spec string, hints ...string) (ref Ref, ok bool) {
	if r == nil {
		return Ref{}, false
	}
	spec = strings.TrimSpace(spec)
	if spec != "" {
		return r.resolveSpec(spec)
	}
	r.mu.RLock()
	var found *entry
	for _, h := range hints {
		for _, k := range variants(h) {
			if e, ok := r.byKey[k]; ok && found == nil {
				found = &e
			}
		}
	}
	r.mu.RUnlock()
	if found == nil {
		return Ref{}, false
	}
	return r.ref(*found), true
}

func (r *Resolver) resolveSpec(spec string) (Ref, bool) {
	lower := strings.ToLower(spec)
	switch {
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
		return Ref{Src: spec}, true
	case strings.HasPrefix(spec, "/") && !strings.HasPrefix(spec, "//"):
		return Ref{Src: spec}, true
	}
	if name, ok := strings.CutPrefix(lower, "mdi-"); ok && validName.MatchString(name) {
		return Ref{Src: "/icons/mdi/" + strings.TrimSuffix(name, ".svg") + ".svg", Mono: true}, true
	}
	if name, ok := strings.CutPrefix(lower, "si-"); ok && validName.MatchString(name) {
		return Ref{Src: "/icons/si/" + strings.TrimSuffix(name, ".svg") + ".svg", Mono: true}, true
	}
	name, format := lower, ""
	for _, ext := range []string{".svg", ".png", ".webp"} {
		if n, ok := strings.CutSuffix(lower, ext); ok {
			name, format = n, ext[1:]
		}
	}
	r.mu.RLock()
	e, ok := r.byName[name]
	if !ok {
		e, ok = r.byKey[key(name)]
	}
	if !ok {
		e, ok = r.byAlias[key(name)]
	}
	r.mu.RUnlock()
	if !ok {
		return Ref{}, false
	}
	if format != "" && format != e.format {
		// Pidió otro formato: dashboard-icons publica PNG y WebP de todos,
		// así que se respeta, sin variantes.
		return Ref{Src: diPath(e.name, format)}, true
	}
	return r.ref(e), true
}

// ref arma el Ref de un logo. Con variantes clara y oscura, la más clara va
// sobre fondo oscuro: si las dos están en disco (SVG), se mide; si no, se
// usa el nombre (variants2) y se bajan para la próxima vez.
func (r *Resolver) ref(e entry) Ref {
	if e.onLight == "" {
		return Ref{Src: diPath(e.name, e.format)}
	}
	onLight, onDark := e.onLight, e.onDark
	if e.format == "svg" {
		la, oka := r.svgLuma(onLight)
		lb, okb := r.svgLuma(onDark)
		if oka && okb && la > lb+0.05 {
			onLight, onDark = onDark, onLight
		}
	}
	return Ref{Src: diPath(onLight, e.format), SrcDark: diPath(onDark, e.format)}
}

// svgLuma devuelve la luminancia media (0 negro, 1 blanco) de los colores
// de un SVG de dashboard-icons que está en disco. Si no está, lo baja en
// segundo plano y devuelve false.
func (r *Resolver) svgLuma(name string) (float64, bool) {
	r.lumaMu.Lock()
	defer r.lumaMu.Unlock()
	if l, ok := r.luma[name]; ok {
		return l, true
	}
	local := filepath.Join(r.dir, "di", name+".svg")
	if raw, err := os.ReadFile(local); err == nil {
		l := svgLuminance(raw)
		r.luma[name] = l
		return l, true
	}
	if !r.fetching[name] {
		r.fetching[name] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
			defer cancel()
			_ = r.download(ctx, r.upstream["di"]+"/svg/"+name+".svg", local)
			r.lumaMu.Lock()
			delete(r.fetching, name)
			r.lumaMu.Unlock()
		}()
	}
	return 0, false
}

var svgColor = regexp.MustCompile(`(?i)(?:fill|stroke|stop-color)\s*[=:]\s*["']?\s*(#[0-9a-f]{6}\b|#[0-9a-f]{3}\b|white|black)`)

// svgLuminance promedia la luminancia de los colores de relleno, trazo y
// degradé que nombra el SVG. Sin ninguno, el relleno por defecto de SVG es
// negro.
func svgLuminance(raw []byte) float64 {
	var sum float64
	n := 0
	for _, m := range svgColor.FindAllSubmatch(raw, -1) {
		c := strings.ToLower(string(m[1]))
		var rr, gg, bb float64
		switch c {
		case "white":
			rr, gg, bb = 1, 1, 1
		case "black":
		default:
			h := c[1:]
			if len(h) == 3 {
				h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
			}
			v, _ := strconv.ParseUint(h, 16, 32)
			rr, gg, bb = float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255
		}
		sum += 0.2126*rr + 0.7152*gg + 0.0722*bb
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func diPath(name, format string) string { return "/icons/di/" + name + "." + format }

// noise son palabras que las imágenes y los nombres agregan al de la
// aplicación: "immich-server", "debian-server", "paperless-ngx-app".
var noise = map[string]bool{"server": true, "app": true, "web": true, "ce": true, "oss": true, "docker": true, "service": true, "daemon": true, "frontend": true, "backend": true}

// variants son las claves con que se busca un nombre: el nombre entero,
// sin la palabra de relleno del final, sin un número de versión al final
// ("jdownloader-2" → "jdownloader2", "jdownloader") y la primera palabra.
func variants(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(strings.TrimSuffix(s, ".service"), ".timer")
	if s == "" {
		return nil
	}
	var out []string
	add := func(k string) {
		if len(k) >= 3 && !contains(out, k) {
			out = append(out, k)
		}
	}
	add(key(s))
	words := strings.FieldsFunc(s, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') })
	for len(words) > 1 && (noise[words[len(words)-1]] || isNumber(words[len(words)-1])) {
		words = words[:len(words)-1]
		add(strings.Join(words, ""))
	}
	add(strings.TrimRight(key(s), "0123456789"))
	// Último recurso, la primera palabra: "restic-backup" → "restic".
	if len(words) > 1 && len(words[0]) >= 4 && !noise[words[0]] {
		add(words[0])
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// ImageHints son los nombres que da una imagen de contenedor, del más al
// menos específico: "ghcr.io/immich-app/immich-server:release" da
// "immich-server" e "immich-app"; "lscr.io/linuxserver/syncthing" da
// "syncthing" (los publicadores de imágenes de otros no cuentan).
func ImageHints(image string) []string {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	parts := strings.Split(image, "/")
	if len(parts) > 1 && strings.ContainsAny(parts[0], ".:") {
		parts = parts[1:] // registro
	}
	var out []string
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if p == "" || publishers[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// publishers son quienes empaquetan imágenes de otros: su nombre no dice
// qué aplicación es.
var publishers = map[string]bool{"library": true, "linuxserver": true, "lscr": true, "hotio": true, "bitnami": true, "jlesage": true, "binhex": true, "11notes": true, "home-operations": true, "onedr0p": true}

// ServeHTTP sirve /icons/<colección>/<archivo>: del disco, o lo baja y lo
// guarda. Va montado con el prefijo /icons/ ya sacado.
func (r *Resolver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	set, file, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
	ext := filepath.Ext(file)
	name := strings.TrimSuffix(file, ext)
	if !validName.MatchString(name) || strings.Contains(name, "..") {
		http.NotFound(w, req)
		return
	}
	var upstream string
	switch {
	case set == "di" && (ext == ".svg" || ext == ".png" || ext == ".webp"):
		if !r.known(name) {
			http.NotFound(w, req)
			return
		}
		upstream = r.upstream[set] + "/" + ext[1:] + "/" + file
	case (set == "mdi" || set == "si") && ext == ".svg":
		upstream = r.upstream[set] + "/" + file
	default:
		http.NotFound(w, req)
		return
	}

	local := filepath.Join(r.dir, set, file)
	f, err := os.Open(local)
	if errors.Is(err, os.ErrNotExist) {
		err = r.download(req.Context(), upstream, local)
		if err == nil {
			f, err = os.Open(local)
		}
	}
	if err != nil {
		r.logger.Debug("íconos: no disponible", "file", set+"/"+file, "err", err)
		http.NotFound(w, req)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "public, max-age=604800")
	h.Set("X-Content-Type-Options", "nosniff")
	// Un SVG abierto como página podría correr scripts con el origen del
	// hub: con sandbox no corre nada. Dentro de <img> no corren nunca.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	switch ext {
	case ".svg":
		h.Set("Content-Type", "image/svg+xml")
	case ".png":
		h.Set("Content-Type", "image/png")
	case ".webp":
		h.Set("Content-Type", "image/webp")
	}
	http.ServeContent(w, req, "", fi.ModTime(), f)
}

// known dice si name es un logo de dashboard-icons o una de sus variantes.
// Sin índice todavía, cualquiera que cumpla el formato: jsDelivr responde
// 404 al que no existe.
func (r *Resolver) known(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.byName == nil {
		return true
	}
	if _, ok := r.byName[name]; ok {
		return true
	}
	for _, suf := range []string{"-light", "-dark"} {
		if base, ok := strings.CutSuffix(name, suf); ok {
			if _, ok := r.byName[base]; ok {
				return true
			}
		}
	}
	return false
}

func (r *Resolver) download(ctx context.Context, url, local string) error {
	r.mu.Lock()
	if t, ok := r.failed[url]; ok && time.Since(t) < retryAfter {
		r.mu.Unlock()
		return errors.New("falló hace poco")
	}
	r.mu.Unlock()
	raw, err := r.get(ctx, url, maxFile)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(local), 0o755); err == nil {
			err = writeAtomic(local, raw)
		}
	}
	if err != nil && ctx.Err() == nil {
		r.mu.Lock()
		r.failed[url] = time.Now()
		r.mu.Unlock()
	}
	return err
}

func (r *Resolver) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "maestro (github.com/ImJustDoingMyPart/maestro)")
	res, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s: más de %d bytes", url, limit)
	}
	return raw, nil
}

// writeAtomic escribe en un temporal y lo renombra: quien lee al mismo
// tiempo ve el archivo viejo o el nuevo, nunca uno a medias.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
