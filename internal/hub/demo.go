package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ImJustDoingMyPart/maestro/internal/httpserver"
	"github.com/ImJustDoingMyPart/maestro/internal/icons"
	"github.com/ImJustDoingMyPart/maestro/web"
)

// demoGroups son servicios inventados para mirar la estética sin agents.
var demoGroups = []group{
	{Name: "Archivos", Services: []serviceCard{
		{Name: "Immich", Description: "Fotos · 5 servicios", Icon: "deployed_code", URL: "https://fotos.example.org",
			State: StateDegraded, Reason: "redis: redis reiniciado (exit 137)", Members: []memberRow{
				{Name: "database", State: StateUp},
				{Name: "immich-folder-album-creator", State: StateUp},
				{Name: "immich-machine-learning", State: StateUp},
				{Name: "immich-server", State: StateUp},
				{Name: "redis", State: StateDegraded, Reason: "redis reiniciado (exit 137)"},
			}},
		{Name: "Syncthing", Description: "Sincronización de carpetas", Icon: "sync", URL: "https://sync.example.org", State: StateUp},
	}},
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

// demoFound son descubiertos inventados para mirar la bandeja.
var demoFound = []Discovered{
	{Host: "adguard", Kind: "unit", Name: "AdGuardHome.service", Label: "AdGuardHome.service", State: StateUp, Detail: "AdGuard Home: Network-level blocker", InFile: "adguard"},
	{Host: "adguard", Kind: "unit", Name: "systemd-resolved.service", Label: "systemd-resolved.service", State: StateUp, Detail: "Network Name Resolution"},
	{Host: "pve", Kind: "lxc", Name: "102", Label: "dns", State: StateUp, Detail: "LXC 102 · 1,0 GB de RAM · 1 núcleo"},
	{Host: "pve", Kind: "vm", Name: "100", Label: "Debian-Server", State: StateUp, Detail: "VM 100 · 12 GB de RAM · 8 núcleos"},
	{Host: "vm", Kind: "timer", Name: "restic-backup.timer", Label: "restic-backup.timer", State: StateDown, Reason: "última corrida falló (exit 1)", Detail: "corrió hace 3 horas · próxima en 2 días"},
	{Host: "vm", Kind: "unit", Name: "netbird.service", Label: "netbird.service", State: StateUp, Detail: "NetBird"},
}

// newDemoHandler arma las rutas del modo --demo: la portada con servicios
// falsos, Hardware vacío y los estáticos. Sin login ni agents.
func newDemoHandler(logger *slog.Logger, ic *icons.Resolver) (http.Handler, error) {
	p, err := parsePages(web.Templates, "services", "hardware", "discovered", "muestrario")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	mux.Handle("GET /icons/", http.StripPrefix("/icons", ic))

	// "GET /{$}" solo coincide con "/" exacto; sin {$}, "/" atrapa todo.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		groups := slices.Clone(demoGroups)
		for i := range groups {
			groups[i].Services = slices.Clone(groups[i].Services)
		}
		decorateGroups(ic, groups, nil)
		p.render(w, logger, "services", pageData{
			Title:        "Servicios",
			Active:       "services",
			ShowNav:      true,
			Demo:         true,
			Groups:       groups,
			Summary:      summarize(groups),
			FoundPending: pending(demoFound, map[string]bool{demoFound[4].Key(): true}),
			FoundFailing: 0,
		})
	})
	mux.HandleFunc("GET /discovered", func(w http.ResponseWriter, r *http.Request) {
		found := foundRows(demoFound, map[string]bool{demoFound[4].Key(): true})
		decorateFound(ic, found, nil)
		p.render(w, logger, "discovered", pageData{
			Title: "Descubiertos", Active: "services", ShowNav: true, Demo: true,
			Found: found,
		})
	})
	mux.HandleFunc("GET /muestrario", func(w http.ResponseWriter, r *http.Request) {
		d := muestraData(ic)
		d.Demo = true
		p.render(w, logger, "muestrario", d)
	})
	mux.HandleFunc("GET /hardware", func(w http.ResponseWriter, r *http.Request) {
		p.render(w, logger, "hardware", pageData{Title: "Hardware", Active: "hardware", ShowNav: true, Demo: true})
	})
	return mux, nil
}

// staticHandler sirve web/static. Con no-cache el navegador guarda los
// archivos pero pregunta si cambiaron (responde 304 si no): mientras se
// desarrolla, un cambio en maestro.css se ve sin forzar la recarga. Para
// poder responder 304 hace falta un ETag: los archivos embebidos no tienen
// fecha, y sin él el navegador bajaba todo de nuevo en cada página (la
// fuente de íconos, 464 KB, llegaba tarde y se leían los nombres).
func staticHandler() http.Handler {
	files := http.FileServerFS(web.Static)
	var etags sync.Map // ruta → ETag, calculado la primera vez que se pide
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := staticETag(&etags, r.URL.Path); ok {
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	})
}

// staticETag devuelve el ETag de un archivo de web/static: los primeros
// 16 caracteres del sha256 de su contenido, entre comillas. No hay ETag
// para lo que no es un archivo (un directorio, una ruta que no existe).
func staticETag(cache *sync.Map, name string) (string, bool) {
	if v, ok := cache.Load(name); ok {
		return v.(string), true
	}
	b, err := fs.ReadFile(web.Static, strings.TrimPrefix(name, "/"))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	tag := `"` + hex.EncodeToString(sum[:8]) + `"`
	cache.Store(name, tag)
	return tag, true
}

// ServeDemo sirve el modo --demo en addr hasta que se cancele ctx.
func ServeDemo(ctx context.Context, addr string, logger *slog.Logger) error {
	// El demo no tiene base: los logos van a la caché del usuario.
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	ic := icons.New(filepath.Join(dir, "maestro", "icons"), logger)
	go ic.Run(ctx)
	h, err := newDemoHandler(logger, ic)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, addr, h, logger)
}
