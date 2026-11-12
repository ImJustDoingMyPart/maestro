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

// tile es un valor grande arriba de cada host.
type tile struct {
	ID          string // "tile:cpu": su clave en el modo edición
	Hidden      bool   // oculto desde el modo edición
	Label, Icon string
	Value, Unit string // "129" y "MB": el número va grande y la unidad chica
	Sub         string
	Pct         float64 // para la barra, de 0 a 100
	Warn        bool    // pasa del 90 % (o 80 °C)
}

// splitUnit separa "129 MB" en "129" y "MB".
func splitUnit(s string) (value, unit string) {
	if i := strings.LastIndex(s, " "); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func newTile(id, label, icon, value, sub string, p float64, warn bool) tile {
	v, u := splitUnit(value)
	return tile{ID: "tile:" + id, Label: label, Icon: icon, Value: v, Unit: u, Sub: sub, Pct: p, Warn: warn}
}

type fsRow struct {
	Mount, Device, FSType string
	Used, Total           string
	Pct                   float64
	PctText               string
	Warn                  bool
}

type smartRow struct {
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
	Containers  []containerUse // los que más memoria usan
	MoreCount   int            // cuántos contenedores más corren
	// Hidden son las secciones ocultas desde el modo edición
	// ("section:storage", "section:guests", "section:containers").
	Hidden map[string]bool
}

// maxLines es cuántas líneas tiene como mucho un gráfico: los colores de
// la paleta de web/src/charts.js.
const maxLines = 8

// maxContainerRows es cuántos contenedores se listan por host.
const maxContainerRows = 10

// hardwarePage muestra un host por vez (?host=), con pestañas para pasar a
// los demás. Sin host o con uno que no existe, el primero.
func (s *server) hardwarePage(w http.ResponseWriter, r *http.Request) {
	snap := s.poller.Snapshot()
	hw := buildHardware(snap, time.Now())
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

// buildHardware arma la página con la última foto.
func buildHardware(snap Snapshot, now time.Time) []hostHardware {
	byContainer := map[string]string{} // ID de contenedor → página de su servicio
	for _, svc := range snap.Services {
		for _, c := range svc.Containers {
			byContainer[c.ID] = servicePath(svc.ID)
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
		hh.Containers, hh.MoreCount = topContainers(sys.Containers, byContainer)
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
		if d.TempC != nil {
			r.Temp = fmt.Sprintf("%.0f °C", *d.TempC)
		}
		if d.PowerOnHours != nil {
			r.Hours = fmt.Sprintf("%d h (%s)", *d.PowerOnHours, ago(time.Duration(*d.PowerOnHours)*time.Hour))
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
		} else if d.Reallocated != nil || d.Uncorrectable != nil {
			r.Sectors = "ninguno malo"
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
		r := guestRow{Kind: foundKindLabel(g.Kind), Name: g.Name, VMID: g.VMID, Status: g.Status, Icon: kindIcon(g.Kind), Href: servicePath(d.Key())}
		switch g.Status {
		case "running":
			r.State, r.Status = StateUp, "encendida"
			r.CPU = fmtPct(g.CPU)
			r.Mem = humanBytes(float64(g.Mem)) + " de " + humanBytes(float64(g.MaxMem))
			r.MemPct = pct(float64(g.Mem), float64(g.MaxMem))
		case "stopped":
			r.State, r.Status = StateDown, "apagada"
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

func topContainers(cs []agent.ContainerStats, href map[string]string) ([]containerUse, int) {
	rows := make([]containerUse, 0, len(cs))
	for _, c := range cs {
		rows = append(rows, containerUse{
			Name: c.Name, Href: href[c.ID], cpu: c.CPU, mem: float64(c.Mem),
			CPU: fmtPct(c.CPU), Mem: humanBytes(float64(c.Mem)),
			NetRx: fmtRate(c.NetRx), NetTx: fmtRate(c.NetTx),
		})
	}
	slices.SortFunc(rows, func(a, b containerUse) int { return cmp.Or(cmp.Compare(b.mem, a.mem), cmp.Compare(a.Name, b.Name)) })
	if len(rows) > maxContainerRows {
		return rows[:maxContainerRows], len(rows) - maxContainerRows
	}
	return rows, 0
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
