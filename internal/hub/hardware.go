package hub

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// chartSpec es un gráfico de la página: sus series y en qué unidad.
type chartSpec struct {
	Title  string
	Unit   string // pct, cores, bytes, rate, temp, plain (ver web/src/charts.js)
	Stack  bool
	Series string // JSON [{key, label}] para data-series
	ID     string // "chart:cpu" en Hardware (modo edición); vacío en Métricas
	Hidden bool   // oculto desde el modo edición
}

type chartLine struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

func newChart(title, unit string, lines ...chartLine) chartSpec {
	b, _ := json.Marshal(lines) // no falla: son strings
	return chartSpec{Title: title, Unit: unit, Series: string(b)}
}

// withID le pone la clave del modo edición a un gráfico de Hardware.
func (c chartSpec) withID(id string) chartSpec {
	c.ID = "chart:" + id
	return c
}

// hwCard es una tarjeta de Hardware: los mosaicos de arriba, los sistemas
// de archivos, los discos y las LXC y VMs tienen todos las mismas partes en
// el mismo lugar. Arriba, un ícono, el título, un dato al lado y, si tiene,
// el estado (el punto y la palabra, como en la portada); en el medio, un
// número grande con su unidad; abajo, una barra (vacía si no aplica, para
// que midan lo mismo) y una línea de detalle.
type hwCard struct {
	ID     string // "tile:cpu": su clave en el modo edición; vacío si no se ordena
	Hidden bool   // oculto desde el modo edición
	Icon   string // de Material Symbols
	Logo   *icons.Ref
	Title  string
	Aside  string // al lado del título: "LXC 102"
	Value  string
	Unit   string // "129" y "MB": el número va grande y la unidad chica
	HasBar bool
	Pct    float64 // para la barra, de 0 a 100
	Warn   bool    // pasa del 90 % (o 80 °C)
	Sub    string
	// State y StateLabel: el estado, si tiene ("" si no).
	State      State
	StateLabel string
	StateTitle string
	Href       string
	// Key e IconSet: para "Cambiar ícono" (LXC y VMs).
	Key, IconSet string
}

// tile es un mosaico de arriba: una hwCard que se ordena y se oculta.
type tile = hwCard

// splitUnit separa "129 MB" en "129" y "MB".
func splitUnit(s string) (value, unit string) {
	if i := strings.LastIndex(s, " "); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func newTile(id, label, icon, value, sub string, p float64, warn bool) tile {
	v, u := splitUnit(value)
	return tile{ID: "tile:" + id, Title: label, Icon: icon, Value: v, Unit: u, Sub: sub, HasBar: true, Pct: p, Warn: warn}
}

// itemID arma la clave del modo edición de una tarjeta: lo que no entra en
// hwItem (un espacio en un punto de montaje) pasa a "_".
func itemID(kind, name string) string {
	b := []byte(name)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("._/@-", c) >= 0) {
			b[i] = '_'
		}
	}
	if len(b) > 100 {
		b = b[:100]
	}
	return kind + ":" + string(b)
}

func (f fsRow) card() hwCard {
	return hwCard{ID: itemID("fs", f.Mount), Icon: "folder_open", Title: f.Mount, Value: strings.TrimSuffix(f.PctText, " %"), Unit: "%",
		HasBar: true, Pct: f.Pct, Warn: f.Warn, Sub: f.Used + " de " + f.Total + " · " + f.Device + " · " + f.FSType}
}

func (d smartRow) card() hwCard {
	c := hwCard{ID: itemID("disk", d.Device), Icon: "hard_drive", Title: d.Device, Value: "–", State: d.State, StateTitle: "Autoevaluación SMART: " + d.Verdict}
	switch d.State {
	case StateUp:
		c.StateLabel = "Sano"
	case StateDegraded:
		c.StateLabel = "Sectores malos"
	case StateDown:
		c.StateLabel = "Falla"
	default:
		c.StateLabel = "Sin datos"
	}
	if d.TempC != nil {
		c.Value, c.Unit = fmt.Sprintf("%.0f", *d.TempC), "°C"
		c.HasBar, c.Pct, c.Warn = true, math.Min(100, math.Max(0, *d.TempC)), *d.TempC >= 60
	}
	var sub []string
	for _, s := range []string{d.Model, d.Hours, d.Sectors, d.Note} {
		if s != "" {
			sub = append(sub, s)
		}
	}
	c.Sub = strings.Join(sub, " · ")
	return c
}

func (g guestRow) card() hwCard {
	c := hwCard{ID: itemID("guest", fmt.Sprint(g.VMID)), Icon: g.Icon, Title: g.Name, Aside: fmt.Sprintf("%s %d", g.Kind, g.VMID), Value: "–",
		State: g.State, StateLabel: g.Status, Href: g.Href, Key: g.Key, Logo: g.Logo, IconSet: g.IconSet}
	if g.Mem != "" {
		used, total, _ := strings.Cut(g.Mem, " de ")
		c.Value, c.Unit = splitUnit(used)
		c.HasBar, c.Pct, c.Warn = true, g.MemPct, g.MemPct >= 90
		c.Sub = "RAM de " + total + " · CPU " + g.CPU
	} else {
		c.Sub = "sin datos de uso"
	}
	return c
}

type fsRow struct {
	Mount, Device, FSType string
	Used, Total           string
	Pct                   float64
	PctText               string
	Warn                  bool
}

type smartRow struct {
	TempC                 *float64
	Device, Model, Serial string
	State                 State // up: pasó; down: falló o sectores malos; unknown: sin dato
	Verdict               string
	Temp, Hours           string
	Sectors               string
	Note                  string
}

type guestRow struct {
	Kind, Name, Status string
	Icon               string
	Logo               *icons.Ref
	Key, IconSet       string // clave del descubierto e ícono elegido
	VMID               int
	Href               string // su página (descubierto)
	State              State
	CPU, Mem           string
	MemPct             float64
}

type containerUse struct {
	Name, Href   string
	CPU, Mem     string
	NetRx, NetTx string
	cpu, mem     float64
	rx, tx       float64
}

// stackUse es una fila de "Lo que más memoria usa": un stack de compose
// con la suma de sus contenedores (que se despliegan, como en la portada),
// o un contenedor suelto.
type stackUse struct {
	containerUse
	Logo    *icons.Ref
	Icon    string
	Members []containerUse // vacío: un contenedor suelto
	key     string         // la de "Cambiar ícono", para el logo elegido
	hint    iconHint
}

// hostHardware es un host en la página de Hardware.
type hostHardware struct {
	Name        string
	OK          bool
	NoSystem    bool // el agent no manda métricas (es de antes de la etapa 7)
	Waiting     bool // manda, pero todavía no tiene dos muestras
	Info        agent.HostInfo
	Uptime      string
	At          time.Time
	Errors      []string
	Tiles       []tile
	Charts      []chartSpec
	FS          []fsRow
	FSChart     *chartSpec
	SMART       []smartRow
	Guests      []guestRow
	GuestCharts []chartSpec
	Containers  []stackUse // los stacks y contenedores que más memoria usan
	MoreCount   int        // cuántos más corren
	// Storage son los sistemas de archivos y los discos; GuestCards, las LXC
	// y VMs: como tarjetas, igual que los mosaicos.
	Storage    []hwCard
	GuestCards []hwCard
	// Sections son las secciones, en el orden en que se ven: lo de
	// siempre (almacenamiento primero) o el elegido en el modo edición.
	Sections []hwSection
}

// hwSection es una sección de Hardware que se ordena y se oculta.
type hwSection struct {
	ID     string // "section:storage"
	Title  string
	Hidden bool
}

// sections arma las secciones que tiene el host, en el orden de siempre.
func (h *hostHardware) sections() {
	add := func(id, title string, ok bool) {
		if ok {
			h.Sections = append(h.Sections, hwSection{ID: "section:" + id, Title: title})
		}
	}
	add("storage", "Almacenamiento", len(h.Storage) > 0)
	add("tiles", "Ahora", len(h.Tiles) > 0)
	add("charts", "En el tiempo", len(h.Charts) > 0)
	add("guests", "LXC y VMs", len(h.GuestCards) > 0)
	add("containers", "Lo que más memoria usa", len(h.Containers) > 0)
}

// maxLines es cuántas líneas tiene como mucho un gráfico: los colores de
// la paleta de web/src/charts.js.
const maxLines = 8

// maxContainerRows es cuántas filas (stacks o contenedores sueltos) se
// listan por host.
const maxContainerRows = 10

// hardwarePage muestra un host por vez (?host=), con pestañas para pasar a
// los demás. Sin host o con uno que no existe, el primero.
func (s *server) hardwarePage(w http.ResponseWriter, r *http.Request) {
	snap := s.poller.Snapshot()
	hw := buildHardware(snap, time.Now())
	// Los hosts van en el orden elegido; el primero es el que se ve sin
	// ?host=. Los que no están en el orden (nuevos), al final.
	if st, ok := s.prefs.(hwLayoutStore); ok {
		order, err := st.HWHosts(r.Context())
		if err != nil {
			s.logger.Error("leer el orden de los hosts", "err", err)
		}
		pos := func(name string) int {
			if i := slices.Index(order, name); i >= 0 {
				return i
			}
			return len(order)
		}
		slices.SortStableFunc(hw, func(a, b hostHardware) int { return cmp.Compare(pos(a.Name), pos(b.Name)) })
	}
	chosen := s.chosenIcons(r.Context())
	for i := range hw {
		hw[i].cards(s.icons, chosen)
	}
	sel := r.URL.Query().Get("host")
	if !slices.ContainsFunc(hw, func(h hostHardware) bool { return h.Name == sel }) && len(hw) > 0 {
		sel = hw[0].Name
	}
	editable := false
	if st, ok := s.prefs.(hwLayoutStore); ok && sel != "" {
		editable = true
		layout, err := st.HWLayout(r.Context(), sel)
		if err != nil {
			s.logger.Error("leer el diseño de Hardware", "host", sel, "err", err)
		}
		for i := range hw {
			if hw[i].Name == sel {
				applyHWLayout(&hw[i], layout)
			}
		}
	}
	s.pages.render(w, s.logger, "hardware", pageData{
		Title: "Hardware", Active: "hardware", ShowNav: true,
		Hardware: hw, HWHost: sel, Live: editable,
	})
}

// cards arma las tarjetas de almacenamiento y de LXC y VMs, con el logo de
// cada LXC o VM: el elegido desde la interfaz o el que se adivina por su
// nombre.
func (h *hostHardware) cards(r *icons.Resolver, chosen map[string]string) {
	for _, f := range h.FS {
		h.Storage = append(h.Storage, f.card())
	}
	for _, d := range h.SMART {
		h.Storage = append(h.Storage, d.card())
	}
	for _, g := range h.Guests {
		g.IconSet = chosen[g.Key]
		g.Logo, g.Icon = logoFor(r, iconHint{spec: g.IconSet, hints: []string{g.Name}}, g.Icon)
		h.GuestCards = append(h.GuestCards, g.card())
	}
	for i := range h.Containers {
		c := &h.Containers[i]
		c.Logo, c.Icon = logoFor(r, c.hint.withOverride(chosen[c.key], false), c.Icon)
	}
	h.sections()
}

// buildHardware arma la página con la última foto.
func buildHardware(snap Snapshot, now time.Time) []hostHardware {
	byContainer := map[string]catalog.Service{} // ID de contenedor → su servicio
	for _, svc := range snap.Services {
		for _, c := range svc.Containers {
			byContainer[c.ID] = svc
		}
	}
	shownFound := map[string]bool{}
	for _, svc := range snap.Services {
		if isFound(svc.ID) {
			shownFound[svc.ID] = true
		}
	}

	var out []hostHardware
	for _, h := range snap.Hosts {
		hh := hostHardware{Name: h.Name, OK: h.OK}
		sys, ok := snap.Systems[h.Name]
		if !ok {
			hh.NoSystem = true
			out = append(out, hh)
			continue
		}
		hh.Info, hh.At, hh.Errors = sys.Host, sys.At, sys.Errors
		if sys.Host.UptimeS > 0 {
			hh.Uptime = ago(time.Duration(sys.Host.UptimeS * float64(time.Second)))
		}
		m := sys.Metrics
		if m == nil {
			hh.Waiting = true
		} else {
			hh.Tiles = hostTiles(sys, m)
			hh.Charts = hostCharts(h.Name, m)
			hh.FS, hh.FSChart = fsRows(h.Name, m.FS)
		}
		hh.SMART = smartRows(sys.SMART)
		hh.Guests, hh.GuestCharts = guestRows(h.Name, sys.Guests, shownFound)
		hh.Containers, hh.MoreCount = topStacks(h.Name, sys.Containers, byContainer)
		out = append(out, hh)
	}
	return out
}

func pct(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return math.Min(100, a*100/b)
}

func fmtPct(v float64) string { return strings.Replace(fmt.Sprintf("%.0f %%", v), ".", ",", 1) }

func fmtRate(v float64) string { return humanBytes(v) + "/s" }

func hostTiles(sys agent.System, m *agent.HostMetrics) []tile {
	cores := ""
	if sys.Host.Threads > 0 {
		cores = plural(sys.Host.Threads, "hilo", "hilos")
	}
	memPct := pct(float64(m.MemUsed), float64(m.MemTotal))
	ts := []tile{
		newTile("cpu", "CPU", "speed", fmtPct(m.CPU), cores, m.CPU, m.CPU >= 90),
		newTile("ram", "RAM", "memory_alt", humanBytes(float64(m.MemUsed)), "de "+humanBytes(float64(m.MemTotal)), memPct, memPct >= 90),
	}
	if m.SwapTotal > 0 {
		p := pct(float64(m.SwapUsed), float64(m.SwapTotal))
		ts = append(ts, newTile("swap", "Swap", "swap_horiz", humanBytes(float64(m.SwapUsed)), "de "+humanBytes(float64(m.SwapTotal)), p, p >= 90))
	}
	// La barra de la carga es la carga de 1 minuto sobre los hilos: 100 %
	// es tener todos ocupados.
	load := strings.ReplaceAll(fmt.Sprintf("%.2f · %.2f · %.2f", m.Load[0], m.Load[1], m.Load[2]), ".", ",")
	var loadPct float64
	if sys.Host.Threads > 0 {
		loadPct = pct(m.Load[0], float64(sys.Host.Threads))
	}
	ts = append(ts, newTile("load", "Carga", "monitor_heart", strings.Replace(fmt.Sprintf("%.2f", m.Load[0]), ".", ",", 1), load+" en 1, 5 y 15 min", loadPct, loadPct >= 90))
	if len(m.Temps) > 0 {
		hot := slices.MaxFunc(m.Temps, func(a, b agent.TempValue) int { return cmp.Compare(a.Celsius, b.Celsius) })
		ts = append(ts, newTile("temp", "Temperatura", "thermostat", fmt.Sprintf("%.0f °C", hot.Celsius), "la más alta: "+hot.Chip+" · "+hot.Label, math.Min(100, math.Max(0, hot.Celsius)), hot.Celsius >= 80))
	}
	return ts
}

func hostCharts(host string, m *agent.HostMetrics) []chartSpec {
	charts := []chartSpec{
		newChart("CPU", "pct", chartLine{hostKey(host, "cpu"), "CPU"}).withID("cpu"),
	}
	mem := []chartLine{{hostKey(host, "mem"), "RAM"}}
	if m.SwapTotal > 0 {
		mem = append(mem, chartLine{hostKey(host, "swap"), "Swap"})
	}
	charts = append(charts, newChart("Memoria", "bytes", mem...).withID("mem"))
	var net []chartLine
	for _, n := range m.Net[:min(len(m.Net), maxLines/2)] {
		net = append(net, chartLine{hostKey(host, "net/"+n.Name+"/rx"), n.Name + " recibe"}, chartLine{hostKey(host, "net/"+n.Name+"/tx"), n.Name + " envía"})
	}
	if len(net) > 0 {
		charts = append(charts, newChart("Red", "rate", net...).withID("net"))
	}
	var disk []chartLine
	for _, d := range m.Disks[:min(len(m.Disks), maxLines/2)] {
		disk = append(disk, chartLine{hostKey(host, "disk/"+d.Name+"/r"), d.Name + " lee"}, chartLine{hostKey(host, "disk/"+d.Name+"/w"), d.Name + " escribe"})
	}
	if len(disk) > 0 {
		charts = append(charts, newChart("Discos", "rate", disk...).withID("disk"))
	}
	// Un gráfico tiene como mucho maxLines líneas, una por color de la
	// paleta: de las temperaturas van las más altas.
	hot := slices.Clone(m.Temps)
	slices.SortStableFunc(hot, func(a, b agent.TempValue) int { return cmp.Compare(b.Celsius, a.Celsius) })
	title := "Temperaturas"
	if len(hot) > maxLines {
		hot = hot[:maxLines]
		title = fmt.Sprintf("Temperaturas (las %d más altas de %d)", maxLines, len(m.Temps))
	}
	var temps []chartLine
	for _, t := range hot {
		temps = append(temps, chartLine{hostKey(host, "temp/"+t.Chip+"/"+t.Label), t.Chip + " · " + t.Label})
	}
	if len(temps) > 0 {
		charts = append(charts, newChart(title, "temp", temps...).withID("temp"))
	}
	return charts
}

func fsRows(host string, fs []agent.FSUsage) ([]fsRow, *chartSpec) {
	var rows []fsRow
	var lines []chartLine
	for _, f := range fs {
		p := pct(float64(f.Used), float64(f.Total))
		rows = append(rows, fsRow{Mount: f.Mount, Device: f.Device, FSType: f.FSType,
			Used: humanBytes(float64(f.Used)), Total: humanBytes(float64(f.Total)), Pct: p, PctText: fmtPct(p), Warn: p >= 90})
		lines = append(lines, chartLine{hostKey(host, "fs/"+f.Mount), f.Mount})
	}
	if len(lines) == 0 {
		return rows, nil
	}
	c := newChart("Espacio usado", "bytes", lines...).withID("fs")
	return rows, &c
}

func smartRows(disks []agent.SMARTDisk) []smartRow {
	var out []smartRow
	for _, d := range disks {
		r := smartRow{Device: d.Device, Model: d.Model, Serial: d.Serial, Note: d.Note, State: StateUnknown, Verdict: "sin dato"}
		if d.Passed != nil {
			if *d.Passed {
				r.State, r.Verdict = StateUp, "pasó"
			} else {
				r.State, r.Verdict = StateDown, "falló"
			}
		}
		r.TempC = d.TempC
		if d.TempC != nil {
			r.Temp = fmt.Sprintf("%.0f °C", *d.TempC)
		}
		if d.PowerOnHours != nil {
			r.Hours = ago(time.Duration(*d.PowerOnHours)*time.Hour) + " encendido"
		}
		var bad []string
		add := func(label string, v *int64) {
			if v != nil && *v > 0 {
				bad = append(bad, fmt.Sprintf("%d %s", *v, label))
			}
		}
		add("reasignados", d.Reallocated)
		add("pendientes", d.Pending)
		add("incorregibles", d.Uncorrectable)
		if len(bad) > 0 {
			r.Sectors = strings.Join(bad, " · ")
			if r.State == StateUp {
				r.State = StateDegraded
			}
			r.Sectors = "sectores " + r.Sectors
		} else if d.Reallocated != nil || d.Uncorrectable != nil {
			r.Sectors = "sin sectores malos"
		}
		out = append(out, r)
	}
	return out
}

func guestRows(host string, guests []agent.Guest, shown map[string]bool) ([]guestRow, []chartSpec) {
	var rows []guestRow
	var cpu, mem []chartLine
	for _, g := range guests {
		d := Discovered{Host: host, Kind: g.Kind, Name: fmt.Sprint(g.VMID)}
		r := guestRow{Kind: foundKindLabel(g.Kind), Name: g.Name, VMID: g.VMID, Status: g.Status, Icon: kindIcon(g.Kind), Href: servicePath(d.Key()), Key: d.Key()}
		switch g.Status {
		case "running":
			r.State, r.Status = StateUp, "Encendida"
			r.CPU = fmtPct(g.CPU)
			r.Mem = humanBytes(float64(g.Mem)) + " de " + humanBytes(float64(g.MaxMem))
			r.MemPct = pct(float64(g.Mem), float64(g.MaxMem))
		case "stopped":
			r.State, r.Status = StateDown, "Apagada"
		default:
			r.State = StateDegraded
		}
		rows = append(rows, r)
		label := fmt.Sprintf("%d %s", g.VMID, g.Name)
		cpu = append(cpu, chartLine{guestKey(host, g.VMID, "cpu"), label})
		mem = append(mem, chartLine{guestKey(host, g.VMID, "mem"), label})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	c := newChart("CPU de LXC y VMs (de sus núcleos)", "pct", cpu...).withID("guests-cpu")
	m := newChart("RAM de LXC y VMs", "bytes", mem...).withID("guests-mem")
	m.Stack = true
	return rows, []chartSpec{c, m}
}

// topStacks junta los contenedores de un host por stack de compose (un
// contenedor sin stack va solo) y devuelve los que más memoria usan, con la
// suma de sus contenedores, y cuántos más hay.
func topStacks(host string, cs []agent.ContainerStats, svcs map[string]catalog.Service) ([]stackUse, int) {
	var out []stackUse
	byProject := map[string]int{} // proyecto → índice en out
	for _, c := range cs {
		svc, known := svcs[c.ID]
		u := containerUse{Name: c.Name, cpu: c.CPU, mem: float64(c.Mem), rx: c.NetRx, tx: c.NetTx}
		if known {
			u.Href = servicePath(svc.ID)
		}
		if !known || svc.Project == "" {
			out = append(out, stackUse{containerUse: u, Icon: "deployed_code", hint: iconHint{hints: []string{c.Name}}})
			if known {
				last := &out[len(out)-1]
				last.Name, last.key, last.hint = svc.Name, serviceKey(svc.ID), serviceHint(svc)
			}
			continue
		}
		i, ok := byProject[svc.Project]
		if !ok {
			i = len(out)
			byProject[svc.Project] = i
			out = append(out, stackUse{
				containerUse: containerUse{Name: svc.Project, Href: stackPath(host, svc.Project)},
				Icon:         "stacks", key: stackKey(host, svc.Project), hint: iconHint{hints: []string{svc.Project}},
			})
		}
		g := &out[i]
		g.Members = append(g.Members, u)
		g.cpu, g.mem, g.rx, g.tx = g.cpu+u.cpu, g.mem+u.mem, g.rx+u.rx, g.tx+u.tx
	}
	for i := range out {
		g := &out[i]
		// Un stack de un solo contenedor es una fila común, con el link
		// a su servicio.
		if len(g.Members) == 1 {
			g.Href, g.Members = g.Members[0].Href, nil
		}
		slices.SortFunc(g.Members, byMem)
		for j := range g.Members {
			g.Members[j].format()
		}
		g.format()
	}
	slices.SortFunc(out, func(a, b stackUse) int { return byMem(a.containerUse, b.containerUse) })
	if len(out) > maxContainerRows {
		return out[:maxContainerRows], len(out) - maxContainerRows
	}
	return out, 0
}

func byMem(a, b containerUse) int { return cmp.Or(cmp.Compare(b.mem, a.mem), cmp.Compare(a.Name, b.Name)) }

func (u *containerUse) format() {
	u.CPU, u.Mem, u.NetRx, u.NetTx = fmtPct(u.cpu), humanBytes(u.mem), fmtRate(u.rx), fmtRate(u.tx)
}

// serviceCharts son los gráficos de la pestaña Métricas de un servicio:
// uno por métrica, con una línea por réplica.
func serviceCharts(svc catalog.Service) []chartSpec {
	if len(svc.Containers) == 0 {
		return nil
	}
	lines := func(metric string, suffix string) []chartLine {
		var ls []chartLine
		for _, c := range svc.Containers {
			ls = append(ls, chartLine{containerKey(c.Host, c.Name, metric), c.Name + suffix})
		}
		return ls
	}
	net := append(lines("rx", " recibe"), lines("tx", " envía")...)
	disk := append(lines("r", " lee"), lines("w", " escribe")...)
	return []chartSpec{
		newChart("CPU (100 % es un núcleo)", "cores", lines("cpu", "")...),
		newChart("Memoria", "bytes", lines("mem", "")...),
		newChart("Red", "rate", net...),
		newChart("Disco", "rate", disk...),
	}
}
