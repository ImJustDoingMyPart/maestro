package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
	"github.com/ImJustDoingMyPart/maestro/internal/httpserver"
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

	poller := NewPoller(cfg, logger)
	go poller.Run(ctx)

	h, err := newHandler(poller, poller, auth.NewSessions(st, !opts.Dev, logger), logger)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, opts.Listen, h, logger)
}

// snapshotter es lo que los handlers necesitan del poller.
type snapshotter interface {
	Snapshot() Snapshot
}

type server struct {
	poller   snapshotter
	logs     logOpener
	sessions *auth.Sessions
	pages    pages
	logger   *slog.Logger
}

func newHandler(poller snapshotter, logs logOpener, sessions *auth.Sessions, logger *slog.Logger) (http.Handler, error) {
	p, err := parsePages(web.Templates, "services", "service", "hardware", "login")
	if err != nil {
		return nil, err
	}
	s := &server{poller: poller, logs: logs, sessions: sessions, pages: p, logger: logger}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

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
		card := serviceCard{
			Href:        servicePath(svc.ID),
			Name:        svc.Name,
			Description: describe(svc),
			Icon:        sourceIcon(svc.Source),
			URL:         svc.URL,
			State:       serviceState(svc, snap),
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
	if !snap.At.IsZero() {
		data.Updated = snap.At.Format("15:04:05")
	}
	return data
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
