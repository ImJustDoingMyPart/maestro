package hub

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/ImJustDoingMyPart/maestro/internal/httpserver"
	"github.com/ImJustDoingMyPart/maestro/web"
)

// demoGroups son servicios inventados para mirar la estética sin agents.
var demoGroups = []group{
	{Name: "Media", Services: []serviceCard{
		{Name: "Jellyfin", Description: "Películas y series", Icon: "movie", URL: "https://jellyfin.example.org", State: StateUp},
		{Name: "Sonarr", Description: "Series", Icon: "live_tv", URL: "https://sonarr.example.org", State: StateUp},
		{Name: "Radarr", Description: "Películas", Icon: "theaters", URL: "https://radarr.example.org", State: StateDown},
		{Name: "qBittorrent", Description: "Descargas", Icon: "download", URL: "https://qbit.example.org", State: StateUp},
		{Name: "Frigate", Description: "Cámara de seguridad, se apaga sin la cámara", Icon: "videocam", State: StateUnknown},
	}},
	{Name: "Red", Services: []serviceCard{
		{Name: "AdGuard Home", Description: "DNS y bloqueo de publicidad", Icon: "shield", URL: "https://adguard.example.org", State: StateUp},
		{Name: "Caddy", Description: "Proxy inverso y certificados", Icon: "swap_horiz", State: StateUp},
		{Name: "NetBird", Description: "VPN", Icon: "vpn_lock", State: StateUp},
		{Name: "Router", Description: "Externo, solo check HTTP", Icon: "router", URL: "http://192.168.0.1", State: StateDown},
	}},
	{Name: "Sistema", Services: []serviceCard{
		{Name: "Forgejo", Description: "Repos de stacks y notas", Icon: "code", URL: "https://git.example.org", State: StateUp},
		{Name: "Restic", Description: "Backups, check push", Icon: "backup", State: StateUp},
		{Name: "Homepage", Description: "Portada actual", Icon: "home", URL: "https://home.example.org", State: StateUp},
	}},
}

// newDemoHandler arma las rutas del modo --demo: la portada con servicios
// falsos, Hardware vacío y los estáticos. Sin login ni agents.
func newDemoHandler(logger *slog.Logger) (http.Handler, error) {
	p, err := parsePages(web.Templates, "services", "hardware")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))

	// "GET /{$}" solo coincide con "/" exacto; sin {$}, "/" atrapa todo.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		p.render(w, logger, "services", pageData{
			Title:   "Servicios",
			Active:  "services",
			ShowNav: true,
			Demo:    true,
			Groups:  demoGroups,
			Summary: summarize(demoGroups),
		})
	})
	mux.HandleFunc("GET /hardware", func(w http.ResponseWriter, r *http.Request) {
		p.render(w, logger, "hardware", pageData{Title: "Hardware", Active: "hardware", ShowNav: true, Demo: true})
	})
	return mux, nil
}

// staticHandler sirve web/static. Con no-cache el navegador guarda los
// archivos pero pregunta si cambiaron (responde 304 si no): mientras se
// desarrolla, un cambio en maestro.css se ve sin forzar la recarga.
func staticHandler() http.Handler {
	files := http.FileServerFS(web.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// ServeDemo sirve el modo --demo en addr hasta que se cancele ctx.
func ServeDemo(ctx context.Context, addr string, logger *slog.Logger) error {
	h, err := newDemoHandler(logger)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, addr, h, logger)
}
