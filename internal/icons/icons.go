// Package icons da a cada servicio el logo de su aplicación.
//
// Los logos salen de dashboard-icons (github.com/homarr-labs/dashboard-icons,
// los mismos que usa Homepage) y, para lo que no tiene, de selfh.st/icons
// (prefijo "sh-"); para íconos monocromos, de Material Design Icons
// ("mdi-") y Simple Icons ("si-"). El navegador nunca habla
// con un tercero: pide /icons/<colección>/<archivo> al hub, que lo baja de
// jsDelivr la primera vez y lo guarda en disco al lado de la base. Después
// de eso anda sin Internet.
//
// Un servicio sin ícono declarado igual lo consigue: Resolve prueba con su
// nombre, su proyecto de compose, su imagen o su unidad contra los índices
// de las dos colecciones (que también se guardan en disco y se renuevan una
// vez por semana).
package icons

import (
	"cmp"
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Orígenes: jsDelivr y, si falla (corta los archivos de repos muy grandes
// con un 403), GitHub directo. Los paquetes de npm van con versión fija;
// dashboard-icons y selfh.st no publican versiones y se usa su rama
// principal, como hace Homepage.
var defaultUpstream = map[string][]string{
	"di":  {"https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons", "https://raw.githubusercontent.com/homarr-labs/dashboard-icons/main"},
	"sh":  {"https://cdn.jsdelivr.net/gh/selfhst/icons", "https://raw.githubusercontent.com/selfhst/icons/main"},
	"mdi": {"https://cdn.jsdelivr.net/npm/@mdi/svg@7.4.47/svg"},
	"si":  {"https://cdn.jsdelivr.net/npm/simple-icons@16.37.0/icons"},
}

const (
	maxFile      = 2 << 20  // un logo pesa unos KB; el PNG más grande, cientos
	maxIndex     = 16 << 20 // los índices rondan 1 MB
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

// entry es un logo de una colección.
type entry struct {
	set    string // di o sh
	name   string
	title  string // nombre para mostrar en la búsqueda
	format string // svg, png o webp
	// onLight y onDark son las variantes para fondo claro y oscuro ("" si
	// no tiene).
	onLight, onDark string
}

// spec es cómo se declara el logo en un label o en el archivo.
func (e entry) spec() string {
	if e.set == "sh" {
		return "sh-" + e.name
	}
	return e.name
}

// collection es un catálogo de logos con índice: dashboard-icons (di) o
// selfh.st (sh). di va primero: si las dos tienen un logo, gana di.
type collection struct {
	set       string
	indexPath string // dentro del repo
	indexFile string // en disco
	parse     func([]byte) ([]entry, map[string][]string, error)

	byName  map[string]entry
	byKey   map[string]entry
	byAlias map[string]entry
	all     []entry // para buscar, en orden de nombre
	loaded  time.Time
}

// Resolver resuelve y sirve los íconos. El cero no sirve: usar New.
type Resolver struct {
	dir    string
	client *http.Client
	logger *slog.Logger
	// upstream son los orígenes de cada colección; los tests los cambian
	// por un servidor local.
	upstream map[string][]string

	mu     sync.RWMutex
	cols   []*collection
	failed map[string]time.Time

	lumaMu   sync.Mutex
	luma     map[string]float64 // luminancia media de un SVG en disco, por "set/nombre"
	fetching map[string]bool    // variantes que se están bajando para medirlas
}

// New crea un Resolver que guarda en dir. No toca la red: eso lo hace Run.
func New(dir string, logger *slog.Logger) *Resolver {
	up := map[string][]string{}
	for k, v := range defaultUpstream {
		up[k] = slices.Clone(v)
	}
	return &Resolver{
		dir:      dir,
		client:   &http.Client{Timeout: fetchTimeout},
		logger:   logger,
		upstream: up,
		failed:   map[string]time.Time{},
		luma:     map[string]float64{},
		fetching: map[string]bool{},
		cols: []*collection{
			{set: "di", indexPath: "/metadata.json", indexFile: "dashboard-icons.json", parse: parseDashboardIcons},
			{set: "sh", indexPath: "/index.json", indexFile: "selfhst-icons.json", parse: parseSelfhst},
		},
	}
}

// Run carga los índices (del disco si están, si no de la red) y los renueva
// una vez por semana, hasta que se cancele ctx. Sin índices, Resolve solo
// resuelve los "mdi-", "si-" y las URLs.
func (r *Resolver) Run(ctx context.Context) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		r.logger.Warn("íconos: no se pudo crear la carpeta", "dir", r.dir, "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, c := range r.cols {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runCollection(ctx, c)
		}()
	}
	wg.Wait()
}

func (r *Resolver) runCollection(ctx context.Context, c *collection) {
	if raw, mod, err := r.readIndex(c); err == nil {
		if err := r.setIndex(c, raw, mod); err != nil {
			r.logger.Warn("íconos: índice en disco ilegible", "set", c.set, "err", err)
		}
	}
	for {
		wait := time.Until(r.loadedAt(c).Add(indexMaxAge))
		if wait <= 0 {
			if err := r.fetchIndex(ctx, c); err != nil {
				r.logger.Warn("íconos: no se pudo bajar el índice", "set", c.set, "err", err)
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

func (r *Resolver) col(set string) *collection {
	for _, c := range r.cols {
		if c.set == set {
			return c
		}
	}
	return nil
}

func (r *Resolver) loadedAt(c *collection) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return c.loaded
}

func (r *Resolver) readIndex(c *collection) ([]byte, time.Time, error) {
	p := filepath.Join(r.dir, c.indexFile)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, time.Time{}, err
	}
	raw, err := os.ReadFile(p)
	return raw, fi.ModTime(), err
}

func (r *Resolver) fetchIndex(ctx context.Context, c *collection) error {
	var raw []byte
	var err error
	for _, base := range r.upstream[c.set] {
		if raw, err = r.get(ctx, base+c.indexPath, maxIndex); err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	if err := r.setIndex(c, raw, time.Now()); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir, c.indexFile), raw)
}

func (r *Resolver) setIndex(c *collection, raw []byte, at time.Time) error {
	entries, aliases, err := c.parse(raw)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("índice vacío")
	}
	byName := make(map[string]entry, len(entries))
	byKey := make(map[string]entry, len(entries))
	byAlias := map[string]entry{}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	for _, e := range entries {
		e.set = c.set
		byName[e.name] = e
		k := key(e.name)
		// Si dos nombres se normalizan igual, gana el que ya era la clave
		// ("jdownloader2" sobre un hipotético "jdownloader-2").
		if old, ok := byKey[k]; !ok || old.name != k && e.name == k {
			byKey[k] = e
		}
		for _, a := range aliases[e.name] {
			if ak := key(a); ak != "" {
				if _, ok := byAlias[ak]; !ok {
					byAlias[ak] = e
				}
			}
		}
	}
	all := make([]entry, 0, len(byName))
	for _, e := range entries {
		all = append(all, byName[e.name])
	}
	r.mu.Lock()
	c.byName, c.byKey, c.byAlias, c.all, c.loaded = byName, byKey, byAlias, all, at
	r.mu.Unlock()
	return nil
}

// parseDashboardIcons lee metadata.json de dashboard-icons: {"<nombre>":
// {"base": "svg", "aliases": [...], "colors": {"dark": …, "light": …}}}.
func parseDashboardIcons(raw []byte) ([]entry, map[string][]string, error) {
	var meta map[string]struct {
		Base    string   `json:"base"`
		Aliases []string `json:"aliases"`
		Colors  struct {
			Dark  string `json:"dark"`
			Light string `json:"light"`
		} `json:"colors"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, nil, err
	}
	var out []entry
	aliases := map[string][]string{}
	for name, m := range meta {
		if !validName.MatchString(name) {
			continue
		}
		e := entry{name: name, title: name, format: m.Base}
		if e.format != "png" && e.format != "webp" {
			e.format = "svg"
		}
		if validName.MatchString(m.Colors.Dark) && validName.MatchString(m.Colors.Light) {
			e.onLight, e.onDark = variants2(m.Colors.Dark, m.Colors.Light)
		}
		out = append(out, e)
		aliases[name] = append(m.Aliases, name)
	}
	return out, aliases, nil
}

// parseSelfhst lee index.json de selfh.st/icons: [{"Name": "AIOStreams",
// "Reference": "aiostreams", "SVG": "Yes", "PNG": "Yes", …}]. Sus
// variantes "-light" y "-dark" son monocromas: se usa la de colores.
func parseSelfhst(raw []byte) ([]entry, map[string][]string, error) {
	var list []struct {
		Name, Reference, SVG, PNG, WebP string
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil, err
	}
	var out []entry
	aliases := map[string][]string{}
	for _, m := range list {
		if !validName.MatchString(m.Reference) {
			continue
		}
		e := entry{name: m.Reference, title: m.Name}
		switch {
		case m.SVG == "Yes":
			e.format = "svg"
		case m.PNG == "Yes":
			e.format = "png"
		case m.WebP == "Yes":
			e.format = "webp"
		default:
			continue
		}
		out = append(out, e)
		aliases[m.Reference] = []string{m.Name}
	}
	return out, aliases, nil
}

// variants2 ordena las dos variantes de un logo: cuál va sobre fondo claro
// y cuál sobre oscuro. Las claves "dark" y "light" de metadata.json no
// significan lo mismo en todas las entradas (en frigate "light" es la
// blanca; en ollama, la negra), así que manda el nombre del archivo, como
// dice el README: "-light" es para fondo oscuro y "-dark" para fondo claro.
// Si las dos están en disco, manda la medición (ver ref).
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
// maestro.icon u homepage.icon, icon en maestro.yaml o el elegido en la
// interfaz) y manda si se encuentra; hints son nombres para adivinarlo (el
// del servicio, su proyecto, su imagen), en orden de preferencia. ok es
// false si no hay logo: la interfaz pone el ícono genérico.
//
// Formatos de spec, los mismos que Homepage: "immich" o "immich.png"
// (dashboard-icons, y si no está, selfh.st), "sh-aiostreams" (selfh.st),
// "mdi-dumbbell", "si-github" y una URL. Un nombre que no está en ninguna
// colección no resuelve: el hub lo prueba como Material Symbol.
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
			for _, c := range r.cols {
				if e, ok := c.byKey[k]; ok && found == nil {
					found = &e
				}
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
	sets := []string{"di", "sh"}
	if name, ok := strings.CutPrefix(lower, "sh-"); ok {
		lower, sets = name, []string{"sh"}
	}
	name, format := lower, ""
	for _, ext := range []string{".svg", ".png", ".webp"} {
		if n, ok := strings.CutSuffix(lower, ext); ok {
			name, format = n, ext[1:]
		}
	}
	r.mu.RLock()
	var (
		e  entry
		ok bool
	)
	for _, set := range sets {
		c := r.col(set)
		if e, ok = c.byName[name]; ok {
			break
		}
		if e, ok = c.byKey[key(name)]; ok {
			break
		}
		if e, ok = c.byAlias[key(name)]; ok {
			break
		}
	}
	r.mu.RUnlock()
	if !ok {
		return Ref{}, false
	}
	if format != "" && format != e.format {
		// Pidió otro formato: las dos colecciones publican PNG y WebP de
		// casi todos, así que se respeta, sin variantes.
		return Ref{Src: path(e.set, e.name, format)}, true
	}
	return r.ref(e), true
}

// Hit es un resultado de Search.
type Hit struct {
	Spec  string // lo que se guarda ("adguard-home", "sh-aiostreams")
	Title string
	Ref   Ref
}

// Search busca logos por nombre o alias para el selector de la interfaz:
// primero los que empiezan con q, después los que lo contienen; como mucho
// n. dashboard-icons va primero y de selfh.st solo los que no tiene.
func (r *Resolver) Search(q string, n int) []Hit {
	if r == nil {
		return nil
	}
	k := key(q)
	if k == "" {
		return nil
	}
	r.mu.RLock()
	var prefix, inside []entry
	seen := map[string]bool{}
	for _, c := range r.cols {
		for _, e := range c.all {
			ek := key(e.name)
			if seen[ek] {
				continue
			}
			switch {
			case strings.HasPrefix(ek, k) || strings.HasPrefix(key(e.title), k):
				prefix = append(prefix, e)
				seen[ek] = true
			case strings.Contains(ek, k) || strings.Contains(key(e.title), k):
				inside = append(inside, e)
				seen[ek] = true
			}
		}
	}
	r.mu.RUnlock()
	byLen := func(a, b entry) int {
		return cmp.Or(cmp.Compare(len(a.name), len(b.name)), strings.Compare(a.name, b.name))
	}
	slices.SortStableFunc(prefix, byLen)
	slices.SortStableFunc(inside, byLen)
	var out []Hit
	for _, e := range append(prefix, inside...) {
		if len(out) == n {
			break
		}
		out = append(out, Hit{Spec: e.spec(), Title: e.title, Ref: r.ref(e)})
	}
	return out
}

// ref arma el Ref de un logo. Con variantes clara y oscura, la más clara va
// sobre fondo oscuro: si las dos están en disco (SVG), se mide; si no, se
// usa el nombre (variants2) y se bajan para la próxima vez.
func (r *Resolver) ref(e entry) Ref {
	if e.onLight == "" {
		return Ref{Src: path(e.set, e.name, e.format)}
	}
	onLight, onDark := e.onLight, e.onDark
	if e.format == "svg" {
		la, oka := r.svgLuma(e.set, onLight)
		lb, okb := r.svgLuma(e.set, onDark)
		if oka && okb && la > lb+0.05 {
			onLight, onDark = onDark, onLight
		}
	}
	return Ref{Src: path(e.set, onLight, e.format), SrcDark: path(e.set, onDark, e.format)}
}

// svgLuma devuelve la luminancia media (0 negro, 1 blanco) de los colores
// de un SVG que está en disco. Si no está, lo baja en segundo plano y
// devuelve false.
func (r *Resolver) svgLuma(set, name string) (float64, bool) {
	id := set + "/" + name
	r.lumaMu.Lock()
	defer r.lumaMu.Unlock()
	if l, ok := r.luma[id]; ok {
		return l, true
	}
	local := filepath.Join(r.dir, set, name+".svg")
	if raw, err := os.ReadFile(local); err == nil {
		l := svgLuminance(raw)
		r.luma[id] = l
		return l, true
	}
	if !r.fetching[id] {
		r.fetching[id] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
			defer cancel()
			_ = r.download(ctx, r.urls(set, "svg/"+name+".svg"), local)
			r.lumaMu.Lock()
			delete(r.fetching, id)
			r.lumaMu.Unlock()
		}()
	}
	return 0, false
}

// urls son los orígenes de un archivo de una colección, en orden.
func (r *Resolver) urls(set, rel string) []string {
	var out []string
	for _, base := range r.upstream[set] {
		out = append(out, base+"/"+rel)
	}
	return out
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

func path(set, name, format string) string { return "/icons/" + set + "/" + name + "." + format }

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
	var urls []string
	switch {
	case (set == "di" || set == "sh") && (ext == ".svg" || ext == ".png" || ext == ".webp"):
		if !r.known(set, name) {
			http.NotFound(w, req)
			return
		}
		urls = r.urls(set, ext[1:]+"/"+file)
	case (set == "mdi" || set == "si") && ext == ".svg":
		urls = r.urls(set, file)
	default:
		http.NotFound(w, req)
		return
	}

	local := filepath.Join(r.dir, set, file)
	f, err := os.Open(local)
	if errors.Is(err, os.ErrNotExist) {
		err = r.download(req.Context(), urls, local)
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

// known dice si name es un logo de la colección o una de sus variantes.
// Sin índice todavía, cualquiera que cumpla el formato: el origen responde
// 404 al que no existe.
func (r *Resolver) known(set, name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := r.col(set)
	if c == nil {
		return false
	}
	if c.byName == nil {
		return true
	}
	if _, ok := c.byName[name]; ok {
		return true
	}
	for _, suf := range []string{"-light", "-dark"} {
		if base, ok := strings.CutSuffix(name, suf); ok {
			if _, ok := c.byName[base]; ok {
				return true
			}
		}
	}
	return false
}

// download baja un archivo probando los orígenes en orden y lo guarda en
// local. Si fallan todos, no se vuelve a intentar antes de retryAfter.
func (r *Resolver) download(ctx context.Context, urls []string, local string) error {
	if len(urls) == 0 {
		return errors.New("sin origen")
	}
	id := urls[0]
	r.mu.Lock()
	if t, ok := r.failed[id]; ok && time.Since(t) < retryAfter {
		r.mu.Unlock()
		return errors.New("falló hace poco")
	}
	r.mu.Unlock()
	var (
		raw []byte
		err error
	)
	for _, u := range urls {
		if raw, err = r.get(ctx, u, maxFile); err == nil {
			break
		}
	}
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(local), 0o755); err == nil {
			err = writeAtomic(local, raw)
		}
	}
	if err != nil && ctx.Err() == nil {
		r.mu.Lock()
		r.failed[id] = time.Now()
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
