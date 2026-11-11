// Package collect es el colector del agent: muestrea cada tanto el host
// (/proc y /sys), las métricas de los contenedores, las unidades de
// systemd, las LXC y VMs de Proxmox y el SMART de los discos, y guarda la
// última muestra para que el hub la pida (agent.SystemSource).
//
// Las velocidades (CPU, red, disco) salen de comparar dos muestras
// seguidas, así que la primera muestra no se publica: hasta la segunda,
// System().At es cero.
package collect

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/procfs"
)

// ContainerCounters son los contadores acumulados de un contenedor que
// corre, como los da la API de stats de Docker. El colector saca las
// velocidades comparando dos lecturas.
type ContainerCounters struct {
	ID, Name string
	// CPUUsage es el tiempo de CPU del contenedor y SystemUsage el del
	// host entero, en nanosegundos; OnlineCPUs, los núcleos que ve.
	CPUUsage, SystemUsage uint64
	OnlineCPUs            int
	Mem, MemLimit         uint64
	NetRx, NetTx          uint64
	Read, Write           uint64
}

// DockerStats da los contadores de los contenedores que corren. Lo cumple
// *docker.Client.
type DockerStats interface {
	Stats(ctx context.Context) ([]ContainerCounters, error)
}

// UnitSource da las unidades de systemd. Lo cumple *systemd.Reader.
type UnitSource interface {
	Units(ctx context.Context) ([]agent.Unit, error)
}

// Options configura el colector. Lo que es nil o false no se lee.
type Options struct {
	Interval time.Duration // cada cuánto se muestrea; 15 s si es cero
	Proc     string        // carpeta de /proc; "/proc" si está vacío
	Sys      string        // carpeta de /sys; "/sys" si está vacío
	// HostFS es dónde está montada la raíz del host cuando el agent corre
	// en un contenedor ("/hostfs"): los sistemas de archivos se buscan
	// debajo y se muestran sin ese prefijo. Vacío: el agent corre en el
	// host y se usan sus montajes.
	HostFS  string
	Docker  DockerStats
	Units   UnitSource
	Proxmox bool // lee las LXC y VMs con pvesh
	SMART   bool // lee el SMART de los discos con smartctl (necesita root)
	Logger  *slog.Logger
}

// Collector guarda la última muestra. Se crea con New y corre con Run.
type Collector struct {
	opts Options
	log  *slog.Logger

	// Lo que solo toca la goroutine de Run, sin lock.
	prev       *rawSample
	prevStats  map[string]ContainerCounters
	smartAt    time.Time
	smartCache []agent.SMARTDisk
	guestsAt   time.Time
	guestCache []agent.Guest
	guestErr   string

	mu  sync.RWMutex
	cur agent.System
}

// rawSample son los contadores del host en un momento.
type rawSample struct {
	at    time.Time
	cpu   procfs.CPUTimes
	net   []procfs.NetDev
	disks []procfs.DiskStat
}

// New arma el colector. Los datos que no cambian (modelo de CPU, kernel)
// se leen acá.
func New(opts Options) *Collector {
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Second
	}
	opts.Proc = cmpOr(opts.Proc, "/proc")
	opts.Sys = cmpOr(opts.Sys, "/sys")
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	c := &Collector{opts: opts, log: opts.Logger}
	c.cur.Docker = opts.Docker != nil
	return c
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// System da la última muestra publicada. Cumple agent.SystemSource.
func (c *Collector) System() agent.System {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cur
}

// Run muestrea enseguida y después cada intervalo, hasta que se cancele
// ctx. Un pánico en una vuelta se registra y la siguiente vuelve a probar.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.opts.Interval)
	defer t.Stop()
	for {
		c.safeSample(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) safeSample(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("pánico en el colector", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, c.opts.Interval)
	defer cancel()
	c.sample(ctx, time.Now())
}

// sample toma una muestra y, si hay una anterior, publica la nueva.
func (c *Collector) sample(ctx context.Context, now time.Time) {
	var (
		sys  agent.System
		errs []string
	)
	fail := func(what string, err error) {
		errs = append(errs, what+": "+err.Error())
	}
	sys.Docker = c.opts.Docker != nil
	sys.Host = c.hostInfo()

	// Lo lento (Docker, systemd, pvesh, smartctl) corre al mismo tiempo
	// que la lectura de /proc.
	var (
		wg       sync.WaitGroup
		stats    []ContainerCounters
		statsErr error
		units    []agent.Unit
		unitsErr error
	)
	if c.opts.Docker != nil {
		wg.Go(func() { stats, statsErr = c.opts.Docker.Stats(ctx) })
	}
	if c.opts.Units != nil {
		wg.Go(func() { units, unitsErr = c.opts.Units.Units(ctx) })
	}
	if c.opts.Proxmox && now.Sub(c.guestsAt) >= guestsEvery {
		wg.Go(func() {
			c.guestsAt = now
			g, err := readGuests(ctx)
			c.guestErr = ""
			if err != nil {
				c.guestErr = err.Error()
				return
			}
			c.guestCache = g
		})
	}
	if c.opts.SMART && now.Sub(c.smartAt) >= smartEvery {
		wg.Go(func() {
			c.smartAt = now
			c.smartCache = readSMART(ctx, c.opts.Sys, now)
		})
	}

	raw, err := c.readRaw(now)
	if err != nil {
		fail("proc", err)
	}
	var metrics *agent.HostMetrics
	if raw != nil && c.prev != nil {
		metrics = c.hostMetrics(c.prev, raw)
	}
	if raw != nil {
		c.prev = raw
	}
	wg.Wait()

	if statsErr != nil {
		fail("docker", statsErr)
	} else if c.opts.Docker != nil {
		sys.Containers = c.containerRates(stats, now)
	}
	if unitsErr != nil {
		fail("systemd", unitsErr)
	} else if c.opts.Units != nil {
		sys.Units = units
		if sys.Units == nil {
			sys.Units = []agent.Unit{}
		}
	}
	if c.opts.Proxmox {
		if c.guestErr != "" {
			errs = append(errs, "proxmox: "+c.guestErr)
		}
		sys.Guests = c.guestCache
	}
	if c.opts.SMART {
		sys.SMART = c.smartCache
	}
	sys.Errors = errs
	if metrics == nil {
		// Primera muestra: sin velocidades todavía. Lo demás (unidades,
		// LXC) ya vale, pero At queda en cero.
		c.publish(sys)
		return
	}
	sys.At, sys.Metrics = now, metrics
	c.publish(sys)
}

func (c *Collector) publish(s agent.System) {
	if len(s.Errors) > 0 {
		c.log.Debug("muestra con errores", "errors", s.Errors)
	}
	c.mu.Lock()
	c.cur = s
	c.mu.Unlock()
}

// guestsEvery y smartEvery: pvesh tarda (es Perl) y smartctl no cambia
// rápido, así que no corren en cada muestra.
const (
	guestsEvery = 30 * time.Second
	smartEvery  = 10 * time.Minute
)

func (c *Collector) open(name string) (*os.File, error) {
	return os.Open(filepath.Join(c.opts.Proc, name))
}

func (c *Collector) readFile(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(c.opts.Proc, name))
	return string(b), err
}

// readRaw lee los contadores de /proc. Sin /proc/stat no hay muestra.
func (c *Collector) readRaw(now time.Time) (*rawSample, error) {
	r := &rawSample{at: now}
	f, err := c.open("stat")
	if err != nil {
		return nil, err
	}
	r.cpu, _, err = procfs.ParseStat(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if f, err := c.open("net/dev"); err == nil {
		r.net, _ = procfs.ParseNetDev(f)
		f.Close()
	}
	if f, err := c.open("diskstats"); err == nil {
		r.disks, _ = procfs.ParseDiskstats(f)
		f.Close()
	}
	return r, nil
}

// hostMetrics arma las métricas entre dos muestras, más lo que se lee en
// el momento (memoria, carga, espacio, temperaturas).
func (c *Collector) hostMetrics(prev, cur *rawSample) *agent.HostMetrics {
	el := cur.at.Sub(prev.at)
	m := &agent.HostMetrics{CPU: procfs.CPUPercent(prev.cpu, cur.cpu)}

	if s, err := c.readFile("loadavg"); err == nil {
		m.Load[0], m.Load[1], m.Load[2], _ = procfs.ParseLoadavg(s)
	}
	if f, err := c.open("meminfo"); err == nil {
		if mem, err := procfs.ParseMeminfo(f); err == nil {
			m.MemTotal, m.MemUsed = mem.Total, mem.Used()
			m.SwapTotal, m.SwapUsed = mem.SwapTotal, mem.SwapUsed()
		}
		f.Close()
	}

	before := map[string]procfs.NetDev{}
	for _, d := range prev.net {
		before[d.Name] = d
	}
	for _, d := range cur.net {
		p, ok := before[d.Name]
		if !ok || !ShowInterface(d.Name) {
			continue
		}
		m.Net = append(m.Net, agent.NetRate{Name: d.Name, Rx: procfs.Rate(p.RxBytes, d.RxBytes, el), Tx: procfs.Rate(p.TxBytes, d.TxBytes, el)})
	}
	beforeDisk := map[string]procfs.DiskStat{}
	for _, d := range prev.disks {
		beforeDisk[d.Name] = d
	}
	for _, d := range cur.disks {
		p, ok := beforeDisk[d.Name]
		if !ok || !procfs.WholeDisk(d.Name) {
			continue
		}
		m.Disks = append(m.Disks, agent.DiskRate{Name: d.Name, Read: procfs.Rate(p.ReadBytes, d.ReadBytes, el), Write: procfs.Rate(p.WriteBytes, d.WriteBytes, el)})
	}
	m.FS = c.filesystems()
	if temps, err := procfs.ReadHwmon(os.DirFS(filepath.Join(c.opts.Sys, "class/hwmon"))); err == nil {
		// Dos chips con el mismo nombre (dos módulos de RAM, dos GPUs) dan
		// sensores con el mismo chip y nombre: el segundo lleva " (2)",
		// así cada uno es una serie distinta.
		seen := map[string]int{}
		for _, t := range temps {
			label := t.Label
			k := t.Chip + "/" + t.Label
			if seen[k]++; seen[k] > 1 {
				label = fmt.Sprintf("%s (%d)", t.Label, seen[k])
			}
			m.Temps = append(m.Temps, agent.TempValue{Chip: t.Chip, Label: label, Celsius: t.Celsius})
		}
	}
	return m
}

// ShowInterface dice si una interfaz de red se muestra. Se dejan afuera
// la de loopback y las que crean Docker y Proxmox para cada contenedor o
// invitado (veth, tap, puentes de firewall): son muchas, van y vienen, y
// su tráfico ya pasa por la interfaz física o el puente principal.
func ShowInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"veth", "tap", "fwbr", "fwpr", "fwln", "docker", "br-", "virbr"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// filesystems da el espacio de los sistemas de archivos de disco, uno por
// dispositivo (un btrfs con varios subvolúmenes montados sale una vez, en
// el punto de montaje más corto).
func (c *Collector) filesystems() []agent.FSUsage {
	f, err := c.open("self/mounts")
	if err != nil {
		return nil
	}
	mounts, err := procfs.ParseMounts(f)
	f.Close()
	if err != nil {
		return nil
	}
	prefix := strings.TrimRight(c.opts.HostFS, "/")
	type cand struct {
		m    procfs.Mount
		show string // punto de montaje en el host
	}
	var cands []cand
	for _, m := range mounts {
		if !procfs.RealFS(m.FSType) {
			continue
		}
		show := m.Point
		if prefix != "" {
			rest, ok := strings.CutPrefix(m.Point, prefix)
			if !ok || (rest != "" && rest[0] != '/') {
				continue
			}
			show = cmpOr(rest, "/")
		}
		cands = append(cands, cand{m, show})
	}
	slices.SortStableFunc(cands, func(a, b cand) int { return len(a.show) - len(b.show) })
	seen := map[string]bool{}
	var out []agent.FSUsage
	for _, cd := range cands {
		if seen[cd.m.Device] {
			continue
		}
		seen[cd.m.Device] = true
		total, used, err := statfs(cd.m.Point)
		if err != nil || total == 0 {
			continue
		}
		out = append(out, agent.FSUsage{Mount: cd.show, Device: cd.m.Device, FSType: cd.m.FSType, Total: total, Used: used})
	}
	slices.SortFunc(out, func(a, b agent.FSUsage) int { return strings.Compare(a.Mount, b.Mount) })
	return out
}

// hostInfo lee lo que casi no cambia. Se relee en cada muestra porque es
// barato y el uptime sí cambia.
func (c *Collector) hostInfo() agent.HostInfo {
	var h agent.HostInfo
	if s, err := c.readFile("sys/kernel/hostname"); err == nil {
		h.Hostname = strings.TrimSpace(s)
	}
	if s, err := c.readFile("sys/kernel/osrelease"); err == nil {
		h.Kernel = strings.TrimSpace(s)
	}
	if f, err := c.open("cpuinfo"); err == nil {
		h.CPUModel, h.Threads, _ = procfs.ParseCPUInfo(f)
		f.Close()
	}
	if s, err := c.readFile("uptime"); err == nil {
		if d, err := procfs.ParseUptime(s); err == nil {
			h.UptimeS = d.Seconds()
		}
	}
	return h
}

// containerRates pasa los contadores de Docker a velocidades, comparando
// con los de la muestra anterior. Un contenedor que se ve por primera vez
// (o que reinició: sus contadores volvieron atrás) sale con CPU y
// velocidades en cero hasta la próxima muestra.
func (c *Collector) containerRates(stats []ContainerCounters, now time.Time) []agent.ContainerStats {
	el := c.opts.Interval
	out := make([]agent.ContainerStats, 0, len(stats))
	next := make(map[string]ContainerCounters, len(stats))
	for _, s := range stats {
		next[s.ID] = s
		cs := agent.ContainerStats{ID: s.ID, Name: s.Name, Mem: s.Mem, MemLimit: s.MemLimit}
		if p, ok := c.prevStats[s.ID]; ok {
			cs.CPU = ContainerCPU(p, s)
			cs.NetRx, cs.NetTx = procfs.Rate(p.NetRx, s.NetRx, el), procfs.Rate(p.NetTx, s.NetTx, el)
			cs.Read, cs.Write = procfs.Rate(p.Read, s.Read, el), procfs.Rate(p.Write, s.Write, el)
		}
		out = append(out, cs)
	}
	c.prevStats = next
	slices.SortFunc(out, func(a, b agent.ContainerStats) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ContainerCPU es la cuenta de docker stats: la parte del tiempo de CPU del
// host que usó el contenedor, por los núcleos. 100 es un núcleo entero.
func ContainerCPU(prev, cur ContainerCounters) float64 {
	if cur.CPUUsage < prev.CPUUsage || cur.SystemUsage <= prev.SystemUsage {
		return 0
	}
	cpus := max(cur.OnlineCPUs, 1)
	return float64(cur.CPUUsage-prev.CPUUsage) / float64(cur.SystemUsage-prev.SystemUsage) * float64(cpus) * 100
}

// readSys lee un archivo de /sys sin el salto de línea.
func readSys(sys, name string) string {
	b, err := os.ReadFile(filepath.Join(sys, name))
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(b))
}

// wholeDisks lista los discos enteros de /sys/block.
func wholeDisks(sys string) []string {
	entries, err := fs.ReadDir(os.DirFS(sys), "block")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if procfs.WholeDisk(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
