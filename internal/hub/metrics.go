package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// Claves de las series. Todas empiezan con el tipo y el host:
//
//	h/<host>/cpu, mem, swap, load1
//	h/<host>/net/<interfaz>/rx|tx
//	h/<host>/disk/<disco>/r|w
//	h/<host>/fs/<punto de montaje>        bytes usados
//	h/<host>/temp/<chip>/<sensor>
//	c/<host>/<contenedor>/cpu|mem|rx|tx|r|w
//	g/<host>/<vmid>/cpu|mem               LXC y VMs de un Proxmox
//
// El punto de montaje va último porque tiene "/".
func hostKey(host, rest string) string              { return "h/" + host + "/" + rest }
func containerKey(host, name, metric string) string { return "c/" + host + "/" + name + "/" + metric }
func guestKey(host string, vmid int, metric string) string {
	return "g/" + host + "/" + strconv.Itoa(vmid) + "/" + metric
}

// systemValues pasa una muestra de un host a valores de series.
func systemValues(host string, sys agent.System) map[string]float64 {
	v := map[string]float64{}
	if m := sys.Metrics; m != nil {
		v[hostKey(host, "cpu")] = m.CPU
		v[hostKey(host, "mem")] = float64(m.MemUsed)
		v[hostKey(host, "load1")] = m.Load[0]
		if m.SwapTotal > 0 {
			v[hostKey(host, "swap")] = float64(m.SwapUsed)
		}
		for _, n := range m.Net {
			v[hostKey(host, "net/"+n.Name+"/rx")] = n.Rx
			v[hostKey(host, "net/"+n.Name+"/tx")] = n.Tx
		}
		for _, d := range m.Disks {
			v[hostKey(host, "disk/"+d.Name+"/r")] = d.Read
			v[hostKey(host, "disk/"+d.Name+"/w")] = d.Write
		}
		for _, f := range m.FS {
			v[hostKey(host, "fs/"+f.Mount)] = float64(f.Used)
		}
		for _, t := range m.Temps {
			v[hostKey(host, "temp/"+t.Chip+"/"+t.Label)] = t.Celsius
		}
	}
	for _, c := range sys.Containers {
		v[containerKey(host, c.Name, "cpu")] = c.CPU
		v[containerKey(host, c.Name, "mem")] = float64(c.Mem)
		v[containerKey(host, c.Name, "rx")] = c.NetRx
		v[containerKey(host, c.Name, "tx")] = c.NetTx
		v[containerKey(host, c.Name, "r")] = c.Read
		v[containerKey(host, c.Name, "w")] = c.Write
	}
	for _, g := range sys.Guests {
		if g.Status != "running" {
			continue
		}
		v[guestKey(host, g.VMID, "cpu")] = g.CPU
		v[guestKey(host, g.VMID, "mem")] = float64(g.Mem)
	}
	return v
}

// metricsStore es lo que el registro y la ruta de datos usan de la base.
type metricsStore interface {
	AddMetrics(ctx context.Context, at time.Time, values map[string]float64) error
	Metrics(ctx context.Context, keys []string, res int, since time.Time) (map[string][]store.MetricPoint, error)
	CompactMetrics(ctx context.Context, now time.Time, r store.MetricRetention) (int64, error)
}

// recorder guarda las muestras de los hosts. Cada muestra se guarda una
// vez: el poller pide cada 15 s y el agent muestrea cada 15 s, pero no
// están sincronizados, así que a veces llega la misma dos veces.
type recorder struct {
	store  metricsStore
	logger *slog.Logger

	mu     sync.Mutex
	lastAt map[string]time.Time
}

func newRecorder(st metricsStore, logger *slog.Logger) *recorder {
	return &recorder{store: st, logger: logger, lastAt: map[string]time.Time{}}
}

// Record guarda las muestras nuevas de la foto.
func (r *recorder) Record(snap Snapshot) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("pánico al guardar métricas", "panic", p, "stack", string(debug.Stack()))
		}
	}()
	r.mu.Lock()
	defer r.mu.Unlock()
	for host, sys := range snap.Systems {
		if sys.At.IsZero() || !sys.At.After(r.lastAt[host]) {
			continue
		}
		r.lastAt[host] = sys.At
		ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		err := r.store.AddMetrics(ctx, sys.At, systemValues(host, sys))
		cancel()
		if err != nil {
			r.logger.Error("guardar métricas", "host", host, "err", err)
		}
	}
}

// Run compacta las series cada hora hasta que se cancele ctx.
func (r *recorder) Run(ctx context.Context) {
	t := time.NewTicker(compactEvery)
	defer t.Stop()
	for {
		n, err := r.store.CompactMetrics(ctx, time.Now(), store.DefaultRetention)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("compactar métricas", "err", err)
		} else if n > 0 {
			r.logger.Info("métricas compactadas", "points", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// metricRanges son los rangos que ofrecen los gráficos y la resolución con
// que se piden: unos cientos o miles de puntos como mucho.
var metricRanges = map[string]struct {
	span time.Duration
	res  int
}{
	"1h":  {time.Hour, store.ResMinute},
	"6h":  {6 * time.Hour, store.ResMinute},
	"24h": {24 * time.Hour, store.ResMinute},
	"7d":  {7 * 24 * time.Hour, store.Res10Min},
	"30d": {30 * 24 * time.Hour, store.ResHour},
	"1y":  {365 * 24 * time.Hour, store.ResHour},
}

// metricsData es la respuesta de /data/metrics, en el formato que uPlot
// usa: una sola lista de horas (segundos Unix) y, por serie, los valores
// alineados a esa lista, con null donde no hay dato. Entre dos puntos
// separados por más del doble de la resolución va un null, para que la
// línea se corte en el hueco (el agent caído) en vez de unir los extremos.
type metricsData struct {
	Range  string         `json:"range"`
	Res    int            `json:"res"`
	T      []int64        `json:"t"`
	Series []metricSeries `json:"series"`
}

type metricSeries struct {
	Key string     `json:"key"`
	Avg []*float64 `json:"avg"`
	Max []*float64 `json:"max"`
}

// alignMetrics arma la respuesta con los puntos de cada serie, en el orden
// de keys.
func alignMetrics(keys []string, pts map[string][]store.MetricPoint, res int) metricsData {
	var ts []int64
	for _, k := range keys {
		for _, p := range pts[k] {
			ts = append(ts, p.At.Unix())
		}
	}
	slices.Sort(ts)
	ts = slices.Compact(ts)
	// Huecos: un instante sin datos en medio.
	var withGaps []int64
	for i, t := range ts {
		if i > 0 && t-ts[i-1] > int64(2*res) {
			withGaps = append(withGaps, ts[i-1]+int64(res))
		}
		withGaps = append(withGaps, t)
	}
	idx := make(map[int64]int, len(withGaps))
	for i, t := range withGaps {
		idx[t] = i
	}
	out := metricsData{Res: res, T: withGaps, Series: make([]metricSeries, 0, len(keys))}
	if out.T == nil {
		out.T = []int64{}
	}
	for _, k := range keys {
		s := metricSeries{Key: k, Avg: make([]*float64, len(withGaps)), Max: make([]*float64, len(withGaps))}
		for _, p := range pts[k] {
			i := idx[p.At.Unix()]
			avg, mx := round(p.Avg), round(p.Max)
			s.Avg[i], s.Max[i] = &avg, &mx
		}
		out.Series = append(out.Series, s)
	}
	return out
}

// round deja tres cifras decimales: más no se ve y engorda el JSON.
func round(v float64) float64 { return math.Round(v*1000) / 1000 }

// maxMetricKeys acota cuántas series se piden de una vez.
const maxMetricKeys = 64

// metricsHandler atiende GET /data/metrics?range=24h&key=…&key=…
func (s *server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.Error(w, "sin métricas", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	rng := q.Get("range")
	if rng == "" {
		rng = "24h"
	}
	spec, ok := metricRanges[rng]
	if !ok {
		http.Error(w, "rango inválido", http.StatusBadRequest)
		return
	}
	keys := q["key"]
	if len(keys) == 0 || len(keys) > maxMetricKeys {
		http.Error(w, "entre 1 y 64 series", http.StatusBadRequest)
		return
	}
	for _, k := range keys {
		if len(k) > 512 || !(strings.HasPrefix(k, "h/") || strings.HasPrefix(k, "c/") || strings.HasPrefix(k, "g/")) {
			http.Error(w, "serie inválida", http.StatusBadRequest)
			return
		}
	}
	pts, err := s.metrics.Metrics(r.Context(), keys, spec.res, time.Now().Add(-spec.span))
	if err != nil {
		s.logger.Error("leer métricas", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	data := alignMetrics(keys, pts, spec.res)
	data.Range = rng
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(data)
}
