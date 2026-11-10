package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
		logger.Warn("no hay usuarios: nadie va a poder entrar. Crealo con: maestro passwd --db " + opts.DBPath)
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

	h, err := newHandler(poller, poller, poller, mon, auth.NewSessions(st, !opts.Dev, logger), logger)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, opts.Listen, h, logger)
}

// snapshotter es lo que los handlers necesitan del poller.
type snapshotter interface {
	Snapshot() Snapshot
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
	runs     *runStore
	sessions *auth.Sessions
	pages    pages
	logger   *slog.Logger
}

func newHandler(poller snapshotter, logs logOpener, stacks stackClient, mon monitorView, sessions *auth.Sessions, logger *slog.Logger) (http.Handler, error) {
	p, err := parsePages(web.Templates, "services", "service", "stack", "hardware", "login")
	if err != nil {
		return nil, err
	}
	s := &server{poller: poller, logs: logs, stacks: stacks, mon: mon, runs: newRunStore(), sessions: sessions, pages: p, logger: logger}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	// Push: lo llaman scripts, sin sesión. El token es la credencial.
	mux.HandleFunc("GET /push/{token}", s.push)
	mux.HandleFunc("POST /push/{token}", s.push)

	private := http.NewServeMux()
	private.HandleFunc("GET /{$}", s.servicesPage)
	private.HandleFunc("GET /fragments/services", s.servicesFragment)
	private.HandleFunc("GET /hardware", s.hardwarePage)
	// El ID de un servicio de compose lleva "/" ("media-stack/jellyfin"),
	// así que va al final de la ruta con {id...}, que toma el resto. Por
	// eso la acción va antes del ID (/logs/<id>) y no después: con
	// /services/<id>/logs, "a/logs" sería ambiguo.
	private.HandleFunc("GET /services/{id...}", s.servicePage)
	private.HandleFunc("GET /logs/{id...}", s.streamLogs)
	private.HandleFunc("GET /fragments/checks/{id...}", s.checksFragment)
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
	data := s.servicesData()
	data.Title, data.Active, data.ShowNav = "Servicios", "services", true
	s.pages.render(w, s.logger, "services", data)
}

func (s *server) servicesFragment(w http.ResponseWriter, r *http.Request) {
	s.pages.renderFragment(w, s.logger, "services", "grid", s.servicesData())
}

func (s *server) hardwarePage(w http.ResponseWriter, r *http.Request) {
	s.pages.render(w, s.logger, "hardware", pageData{Title: "Hardware", Active: "hardware", ShowNav: true})
}

// servicesData arma la grilla desde la última foto del poller.
func (s *server) servicesData() pageData {
	snap := s.poller.Snapshot()
	data := pageData{Live: true}

	var others *group // los servicios sin grupo van al final
	for _, svc := range snap.Services {
		st := s.mon.Status(svc.ID)
		card := serviceCard{
			Href:        servicePath(svc.ID),
			Name:        svc.Name,
			Description: describe(svc),
			Icon:        sourceIcon(svc.Source),
			URL:         svc.URL,
			State:       State(st.State),
			Reason:      st.Reason(),
		}
		if svc.Group == "" {
			if others == nil {
				others = &group{Name: "Otros"}
			}
			others.Services = append(others.Services, card)
			continue
		}
		// catalog ya los ordena por grupo: alcanza con mirar el último.
		if n := len(data.Groups); n == 0 || data.Groups[n-1].Name != svc.Group {
			data.Groups = append(data.Groups, group{Name: svc.Group})
		}
		g := &data.Groups[len(data.Groups)-1]
		g.Services = append(g.Services, card)
	}
	if others != nil {
		data.Groups = append(data.Groups, *others)
	}
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
	s.pages.render(w, s.logger, "login", pageData{Title: "Entrar"})
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
