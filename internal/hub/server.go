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

	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
	"github.com/ImJustDoingMyPart/maestro/internal/httpserver"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
	"github.com/ImJustDoingMyPart/maestro/web"
)

// Options configura "maestro serve".
type Options struct {
	Listen     string
	ConfigPath string
	DBPath     string
	// Dev desactiva el atributo Secure de la cookie, para probar por HTTP
	// sin Caddy. Nunca en producción.
	Dev bool
}

// Serve corre el hub hasta que se cancele ctx.
func Serve(ctx context.Context, opts Options, logger *slog.Logger) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, opts.DBPath)
	if err != nil {
		return fmt.Errorf("base %s: %w", opts.DBPath, err)
	}
	defer st.Close()

	if has, err := st.HasUsers(ctx); err != nil {
		return err
	} else if !has {
		logger.Warn("no hay usuarios: la primera persona que abra la web lo crea (o: maestro passwd --db " + opts.DBPath + ")")
	}
	if opts.Dev {
		logger.Warn("modo --dev: la cookie de sesión viaja sin Secure; usarlo solo en local")
	}

	mon, err := NewMonitor(ctx, st, logger)
	if err != nil {
		return fmt.Errorf("monitor: %w", err)
	}
	go mon.Run(ctx)

	poller := NewPoller(cfg, logger)
	poller.onPoll = mon.Update
	go poller.Run(ctx)

	h, err := newHandler(poller, poller, poller, mon, st, auth.NewSessions(st, !opts.Dev, logger), logger)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, opts.Listen, h, logger)
}

// snapshotter es lo que los handlers necesitan del poller.
type snapshotter interface {
	Snapshot() Snapshot
}

// prefStore guarda la configuración que se hace desde la interfaz. Lo
// cumple *store.Store.
type prefStore interface {
	Hidden(ctx context.Context) (map[string]time.Time, error)
	Hide(ctx context.Context, key string, at time.Time) error
	Unhide(ctx context.Context, key string) error
	Names(ctx context.Context) (map[string]string, error)
	SetName(ctx context.Context, key, name string, at time.Time) error
}

// monitorView es lo que los handlers necesitan del monitor. Lo cumple
// *Monitor; los tests pasan uno falso.
type monitorView interface {
	Status(id string) status.Status
	Checks(id string) []CheckView
	History(ctx context.Context, id string, since time.Time) ([]store.CheckResult, []store.StateEvent, error)
	Subscribe(id string) (<-chan store.StateEvent, func())
	Push(token string) bool
}

type server struct {
	poller   snapshotter
	logs     logOpener
	stacks   stackClient
	mon      monitorView
	prefs    prefStore
	runs     *runStore
	sessions *auth.Sessions
	pages    pages
	logger   *slog.Logger
}

func newHandler(poller snapshotter, logs logOpener, stacks stackClient, mon monitorView, prefs prefStore, sessions *auth.Sessions, logger *slog.Logger) (http.Handler, error) {
	p, err := parsePages(web.Templates, "services", "service", "stack", "hardware", "login", "hidden")
	if err != nil {
		return nil, err
	}
	s := &server{poller: poller, logs: logs, stacks: stacks, mon: mon, prefs: prefs, runs: newRunStore(), sessions: sessions, pages: p, logger: logger}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("POST /logout", s.logout)
	// Push: lo llaman scripts, sin sesión. El token es la credencial.
	mux.HandleFunc("GET /push/{token}", s.push)
	mux.HandleFunc("POST /push/{token}", s.push)

	private := http.NewServeMux()
	private.HandleFunc("GET /{$}", s.servicesPage)
	private.HandleFunc("GET /fragments/services", s.servicesFragment)
	private.HandleFunc("GET /hardware", s.hardwarePage)
	private.HandleFunc("GET /hidden", s.hiddenPage)
	private.HandleFunc("POST /hidden", s.hide)
	private.HandleFunc("POST /hidden/show", s.unhide)
	private.HandleFunc("POST /names", s.rename)
	// El ID de un servicio de compose lleva "/" ("media-stack/jellyfin"),
	// así que va al final de la ruta con {id...}, que toma el resto. Por
	// eso la acción va antes del ID (/logs/<id>) y no después: con
	// /services/<id>/logs, "a/logs" sería ambiguo.
	private.HandleFunc("GET /services/{id...}", s.servicePage)
	private.HandleFunc("GET /logs/{id...}", s.streamLogs)
	private.HandleFunc("GET /fragments/checks/{id...}", s.checksFragment)
	private.HandleFunc("GET /fragments/service/{id...}", s.liveFragment)
	// Un stack se nombra por host y proyecto, no por servicio: después de
	// un down no le quedan contenedores ni servicios, y hay que poder
	// levantarlo de nuevo.
	private.HandleFunc("GET /stacks/{host}/{project}", s.stackPage)
	private.HandleFunc("POST /stacks/{host}/{project}/files/{name}/check", s.checkStackFile)
	private.HandleFunc("POST /stacks/{host}/{project}/files/{name}", s.saveStackFile)
	private.HandleFunc("POST /stacks/{host}/{project}/actions/{action}", s.startAction)
	private.HandleFunc("GET /runs/{id}", s.streamRun)
	mux.Handle("/", sessions.RequireLogin(private))

	// Rechaza POST (y demás métodos no seguros) que vengan de otro origen:
	// mira Sec-Fetch-Site y, si falta, compara Origin con Host. Es la
	// protección CSRF; la cookie SameSite=Lax es la segunda capa.
	csrf := http.NewCrossOriginProtection()
	return securityHeaders(csrf.Handler(mux)), nil
}

// securityHeaders agrega los headers que no dependen de la página.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY") // nadie puede meter el panel en un iframe
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *server) servicesPage(w http.ResponseWriter, r *http.Request) {
	data := s.servicesData(r.Context())
	data.Title, data.Active, data.ShowNav = "Servicios", "services", true
	s.pages.render(w, s.logger, "services", data)
}

func (s *server) servicesFragment(w http.ResponseWriter, r *http.Request) {
	s.pages.renderFragment(w, s.logger, "services", "grid", s.servicesData(r.Context()))
}

func (s *server) hardwarePage(w http.ResponseWriter, r *http.Request) {
	s.pages.render(w, s.logger, "hardware", pageData{Title: "Hardware", Active: "hardware", ShowNav: true})
}

// servicesData arma la grilla desde la última foto del poller.
func (s *server) servicesData(ctx context.Context) pageData {
	snap := s.poller.Snapshot()
	data := pageData{Live: true}

	hidden, err := s.prefs.Hidden(ctx)
	if err != nil {
		s.logger.Error("leer ocultos", "err", err) // se muestra todo
	}
	names := s.names(ctx)
	data.Groups, data.HiddenCount = buildGroups(applyNames(snap.Services, names), s.mon.Status, hidden, names)
	data.Summary = summarize(data.Groups)

	for _, h := range snap.Hosts {
		if !h.OK {
			data.HostProblems = append(data.HostProblems, h)
		}
	}
	data.Updated = snap.At
	return data
}

// push atiende /push/{token}. Responde lo mismo exista o no el token: la
// ruta no tiene login y no tiene que servir para probar tokens. El que no
// existe queda en el log, para ver un script mal configurado.
func (s *server) push(w http.ResponseWriter, r *http.Request) {
	if !s.mon.Push(r.PathValue("token")) {
		s.logger.Warn("push con un token desconocido", "remote", r.RemoteAddr,
			"forwarded_for", r.Header.Get("X-Forwarded-For"))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.sessions.LoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	setup, err := s.sessions.NeedsSetup(r.Context())
	if err != nil {
		s.logger.Error("¿hay usuarios?", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	if setup {
		s.pages.render(w, s.logger, "login", pageData{Title: "Crear usuario", Setup: true})
		return
	}
	s.pages.render(w, s.logger, "login", pageData{Title: "Entrar"})
}

// setup atiende POST /setup: crea el primer usuario y entra con él. Solo
// mientras no haya ninguno; después responde como un login viejo, con el
// formulario de entrar.
func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return
	}
	username, password := r.PostFormValue("username"), r.PostFormValue("password")
	again := func(code int, msg string) {
		s.pages.renderCode(w, s.logger, code, "login", pageData{
			Title: "Crear usuario", Setup: true, Error: msg, Username: username,
		})
	}
	if password != r.PostFormValue("password2") {
		again(http.StatusBadRequest, "Las contraseñas no coinciden.")
		return
	}
	err := s.sessions.Setup(r.Context(), w, username, password)
	switch {
	case errors.Is(err, auth.ErrSetupDone):
		s.pages.renderCode(w, s.logger, http.StatusConflict, "login", pageData{
			Title: "Entrar", Error: "Ya hay un usuario: entrá con él.",
		})
		return
	case errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrBadUsername):
		again(http.StatusBadRequest, capitalize(err.Error())+".")
		return
	case err != nil:
		s.logger.Error("crear el primer usuario", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	// Queda en el log con la IP, como los logins fallidos: es la creación
	// de la única cuenta del panel.
	s.logger.Warn("primer usuario creado desde la web", "user", username, "remote", r.RemoteAddr,
		"forwarded_for", r.Header.Get("X-Forwarded-For"))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// capitalize pone en mayúscula la primera letra de un mensaje de error.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // un login no ocupa más de 4 KiB
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return
	}
	username, password := r.PostFormValue("username"), r.PostFormValue("password")

	err := s.sessions.Login(r.Context(), w, username, password)
	if errors.Is(err, auth.ErrBadCredentials) {
		// Queda en el log con la IP (detrás de Caddy, la de Caddy más el
		// X-Forwarded-For), por si hace falta mirar intentos repetidos.
		s.logger.Warn("login fallido", "user", username, "remote", r.RemoteAddr,
			"forwarded_for", r.Header.Get("X-Forwarded-For"))
		s.pages.renderCode(w, s.logger, http.StatusUnauthorized, "login", pageData{
			Title: "Entrar", Error: "Usuario o contraseña incorrectos.", Username: username,
		})
		return
	}
	if err != nil {
		s.logger.Error("login", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	s.logger.Info("login", "user", username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Logout(w, r); err != nil {
		s.logger.Error("logout", "err", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
