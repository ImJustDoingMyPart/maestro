package hub

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// uiServiceSetter recibe los servicios externos de la interfaz. Lo cumple
// *Poller; en los tests con una foto fija no hay.
type uiServiceSetter interface {
	SetUIServices(map[string]catalog.FileService)
}

// externalForm es el formulario de un servicio externo, con lo que la
// persona escribió (también cuando hay errores).
type externalForm struct {
	ID          string // vacío al crear
	Name        string
	Group       string
	URL         string
	Description string
	Kind        string // "http" o "tcp"
	Target      string
	Expect      string
	Redirects   bool // seguir redirecciones
	Interval    string
	Timeout     string
	Errors      []string
	Groups      []string // para sugerir
}

// externalServices lee los externos de la base en la forma que recibe el
// catálogo.
func externalServices(ctx context.Context, st interface {
	Externals(context.Context) ([]store.External, error)
}) (map[string]catalog.FileService, error) {
	all, err := st.Externals(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]catalog.FileService, len(all))
	for _, e := range all {
		out[e.ID] = catalog.FileService{
			URL: e.URL, Description: e.Description, Checks: externalCheck(e).Checks(), FromUI: true,
		}
	}
	return out, nil
}

// externalCheck pasa el check de un externo a la forma del archivo.
func externalCheck(e store.External) *config.Check {
	c := &config.Check{Expect: e.Expect}
	if e.Kind == "http" {
		c.HTTP = e.Target
		if e.NoRedirects {
			no := false
			c.Redirects = &no
		}
	} else {
		c.TCP = e.Target
	}
	if e.Interval > 0 {
		c.Interval = e.Interval.String()
	}
	if e.Timeout > 0 {
		c.Timeout = e.Timeout.String()
	}
	return c
}

// reloadExternals le pasa al poller los externos de la base.
func (s *server) reloadExternals(ctx context.Context) {
	if s.ui == nil {
		return
	}
	ui, err := externalServices(ctx, s.prefs)
	if err != nil {
		s.logger.Error("leer externos", "err", err)
		return
	}
	s.ui.SetUIServices(ui)
}

func (s *server) groupNames(ctx context.Context) []string {
	var out []string
	for _, g := range s.currentGroups(ctx) {
		if !g.Otros {
			out = append(out, g.Name)
		}
	}
	return out
}

func (s *server) renderExternal(w http.ResponseWriter, r *http.Request, code int, f externalForm) {
	f.Groups = s.groupNames(r.Context())
	title := "Nuevo servicio externo"
	if f.ID != "" {
		title = f.Name
	}
	s.page(w, r, code, pageData{Title: title, Active: "services", ShowNav: true, Ext: &f})
}

// newExternalPage atiende GET /externals/new.
func (s *server) newExternalPage(w http.ResponseWriter, r *http.Request) {
	s.renderExternal(w, r, http.StatusOK, externalForm{Kind: "http", Redirects: true})
}

// editExternalPage atiende GET /externals/{id}.
func (s *server) editExternalPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, err := s.prefs.External(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.page(w, r, http.StatusNotFound, pageData{Title: "No existe"})
		return
	} else if err != nil {
		s.logger.Error("leer externo", "id", id, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	p := s.loadPrefs(r.Context())
	key := serviceKey(id)
	f := externalForm{
		ID: id, Name: cmpOr(p.Names[key], id), Group: p.Groups[key], URL: e.URL, Description: e.Description,
		Kind: e.Kind, Target: e.Target, Redirects: !e.NoRedirects,
	}
	if e.Expect != 0 {
		f.Expect = strconv.Itoa(e.Expect)
	}
	if e.Interval > 0 {
		f.Interval = e.Interval.String()
	}
	if e.Timeout > 0 {
		f.Timeout = e.Timeout.String()
	}
	s.renderExternal(w, r, http.StatusOK, f)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseExternal lee y valida el formulario. Los errores van en f.Errors.
func parseExternal(r *http.Request) (externalForm, store.External) {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	f := externalForm{
		Name: v("name"), Group: v("group"), URL: v("url"), Description: v("description"),
		Kind: v("kind"), Target: v("target"), Expect: v("expect"), Redirects: r.PostFormValue("redirects") == "1",
		Interval: v("interval"), Timeout: v("timeout"),
	}
	bad := func(msg string) { f.Errors = append(f.Errors, msg) }
	switch {
	case f.Name == "":
		bad("Falta el nombre.")
	case utf8.RuneCountInString(f.Name) > 80:
		bad("El nombre puede tener hasta 80 caracteres.")
	}
	if strings.EqualFold(f.Group, otros) {
		f.Group = ""
	}
	if utf8.RuneCountInString(f.Group) > 40 {
		bad("El grupo puede tener hasta 40 caracteres.")
	}
	if utf8.RuneCountInString(f.Description) > 200 {
		bad("La descripción puede tener hasta 200 caracteres.")
	}
	if f.URL != "" {
		if err := config.ValidURL(f.URL); err != nil {
			bad("Link: " + err.Error() + ".")
		} else if hasUserinfo(f.URL) {
			bad("Link: sin usuario ni contraseña en la URL (quedarían en la base y en el export).")
		}
	}

	e := store.External{URL: f.URL, Description: f.Description, Kind: f.Kind, Target: f.Target, NoRedirects: !f.Redirects}
	c := &config.Check{Interval: f.Interval, Timeout: f.Timeout}
	switch f.Kind {
	case "http":
		c.HTTP = f.Target
		if !f.Redirects {
			no := false
			c.Redirects = &no
		}
		if f.Expect != "" {
			n, err := strconv.Atoi(f.Expect)
			if err != nil {
				bad("Código esperado: tiene que ser un número (200, 204, 301…).")
			}
			c.Expect, e.Expect = n, n
		}
		if hasUserinfo(f.Target) {
			bad("Check HTTP: sin usuario ni contraseña en la URL (quedarían en la base y en el export).")
		}
	case "tcp":
		c.TCP = f.Target
		e.NoRedirects, f.Expect = false, ""
	default:
		bad("Elegí un check HTTP o TCP.")
		return f, e
	}
	if err := config.ValidateCheck("x", c); err != nil {
		for _, msg := range strings.Split(err.Error(), "\n") {
			bad(checkMessage(msg))
		}
	}
	e.Interval, _ = time.ParseDuration(cmpOr(f.Interval, "0s"))
	e.Timeout, _ = time.ParseDuration(cmpOr(f.Timeout, "0s"))
	return f, e
}

// checkMessage traduce un error de config.ValidateCheck ("services.x.check.tcp:
// …") a los nombres del formulario.
func checkMessage(msg string) string {
	msg = strings.TrimPrefix(msg, "services.x.")
	for _, r := range []struct{ from, to string }{
		{"check.http:", "Check HTTP:"}, {"check.tcp:", "Check TCP:"}, {"check.expect:", "Código esperado:"},
		{"check.interval:", "Intervalo:"}, {"check.timeout:", "Timeout:"}, {"check:", "Check:"},
	} {
		if rest, ok := strings.CutPrefix(msg, r.from); ok {
			return r.to + rest + "."
		}
	}
	return msg
}

func hasUserinfo(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.User != nil
}

// sinAcento pasa las letras acentuadas más comunes a ASCII. No hace falta
// más: el ID solo se ve en las URLs y un carácter que no está acá pasa a
// guion.
var sinAcento = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
	"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u", "ç", "c", "ã", "a", "õ", "o", "â", "a", "ê", "e", "ô", "o")

// slugify arma el ID de un externo desde su nombre: minúsculas, sin
// acentos, letras, números y guiones.
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range sinAcento.Replace(strings.ToLower(name)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// freeID da un ID libre a partir de base: ni un servicio que ya existe ni
// una clave del archivo (aunque esté ignorada).
func (s *server) freeID(ctx context.Context, base string) (string, error) {
	base = cmpOr(base, "externo")
	if len(base) > 60 {
		base = strings.Trim(base[:60], "-")
	}
	taken := map[string]bool{}
	for _, svc := range s.poller.Snapshot().Services {
		taken[svc.ID] = true
	}
	for k := range s.fileKeys {
		taken[k] = true
	}
	all, err := s.prefs.Externals(ctx)
	if err != nil {
		return "", err
	}
	for _, e := range all {
		taken[e.ID] = true
	}
	id := base
	for i := 2; taken[id]; i++ {
		id = base + "-" + strconv.Itoa(i)
	}
	return id, nil
}

// createExternal atiende POST /externals.
func (s *server) createExternal(w http.ResponseWriter, r *http.Request) {
	if !parseFormLimit(w, r, 16<<10) {
		return
	}
	f, e := parseExternal(r)
	if len(f.Errors) > 0 {
		s.renderExternal(w, r, http.StatusBadRequest, f)
		return
	}
	id, err := s.freeID(r.Context(), slugify(f.Name))
	if err == nil {
		e.ID = id
		err = s.prefs.CreateExternal(r.Context(), e, f.Name, f.Group, time.Now())
	}
	if err != nil {
		s.logger.Error("crear externo", "err", err)
		http.Error(w, "no se pudo crear el servicio", http.StatusInternalServerError)
		return
	}
	s.reloadExternals(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// updateExternal atiende POST /externals/{id}.
func (s *server) updateExternal(w http.ResponseWriter, r *http.Request) {
	if !parseFormLimit(w, r, 16<<10) {
		return
	}
	f, e := parseExternal(r)
	f.ID, e.ID = r.PathValue("id"), r.PathValue("id")
	if len(f.Errors) > 0 {
		s.renderExternal(w, r, http.StatusBadRequest, f)
		return
	}
	err := s.prefs.UpdateExternal(r.Context(), e, f.Name, f.Group, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.logger.Error("editar externo", "id", e.ID, "err", err)
		http.Error(w, "no se pudo guardar", http.StatusInternalServerError)
		return
	}
	s.reloadExternals(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// deleteExternal atiende POST /externals/{id}/delete. Sus resultados de
// checks quedan en la base hasta que la compactación los borre.
func (s *server) deleteExternal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.prefs.DeleteExternal(r.Context(), id); err != nil {
		s.logger.Error("borrar externo", "id", id, "err", err)
		http.Error(w, "no se pudo borrar", http.StatusInternalServerError)
		return
	}
	s.reloadExternals(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// parseFormLimit lee un formulario de hasta limit bytes.
func parseFormLimit(w http.ResponseWriter, r *http.Request, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return false
	}
	return true
}
