package hub

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

const (
	// historySpan es cuánto historial de checks muestra la página.
	historySpan = 24 * time.Hour
	// historyBars es en cuántas barras se reparte: media hora cada una.
	historyBars = 48
)

// checkPanel es un check en la página del servicio.
type checkPanel struct {
	Kind     string // "HTTP", "TCP", "Push"
	Target   string // vacío en push: es el token
	State    State
	Last     string    // detalle del último resultado
	LastAt   time.Time // cero si todavía no corrió
	Latency  string    // del último resultado; vacío en push
	Uptime   string    // disponibilidad en historySpan; vacío sin resultados
	Cert     string    // "certificado hasta el 2027-02-01 (83 días)"; vacío si no aplica
	CertWarn bool
	Bars     []bar
}

// bar es una barra del historial.
type bar struct {
	Class    string // "up", "down", "partial" o "none"
	From, To time.Time
	Summary  string // "3 bien, 1 falla"
}

// eventRow es un cambio de estado para mostrar.
type eventRow struct {
	At     time.Time
	State  State
	Reason string
}

// checksFragment atiende /fragments/checks/{id...}: el panel de checks que
// la página del servicio vuelve a pedir cada minuto.
func (s *server) checksFragment(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookup(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "servicio inexistente", http.StatusNotFound)
		return
	}
	d := detail(svc, s.mon.Status(svc.ID))
	d.Logo, d.Icon = logoFor(s.icons, serviceHint(svc).withOverride(s.chosenIcons(r.Context())[serviceKey(svc.ID)], svc.IconFromFile), d.Icon)
	d.ChecksURL = "/fragments/checks/" + escapeID(svc.ID)
	d.Checks, _ = s.checkPanels(r, svc)
	s.pages.renderFragment(w, s.logger, "service", "checks", pageData{Svc: &d})
}

// checkPanels arma los paneles de los checks del servicio y los cambios de
// estado de historySpan.
func (s *server) checkPanels(r *http.Request, svc catalog.Service) ([]checkPanel, []eventRow) {
	now := time.Now()
	start := now.Add(-historySpan)
	views := s.mon.Checks(svc.ID)
	// Un push bueno de antes de la ventana puede cubrir parte de ella: se
	// lee desde un plazo antes.
	lookback := time.Duration(0)
	for _, v := range views {
		lookback = max(lookback, v.Deadline)
	}
	results, events, err := s.mon.History(r.Context(), svc.ID, start.Add(-lookback))
	if err != nil {
		s.logger.Error("leer historial", "service", svc.ID, "err", err)
	}
	var rows []eventRow
	for i := len(events) - 1; i >= 0; i-- { // el más nuevo primero
		e := events[i]
		if e.At.Before(start) {
			break
		}
		rows = append(rows, eventRow{At: e.At, State: State(e.To), Reason: shownReason(e.Reason)})
	}

	var panels []checkPanel
	for _, v := range views {
		p := checkPanel{Kind: kindLabel(v.Kind), Target: v.Target, State: checkLight(v)}
		if v.Results > 0 {
			p.Last, p.LastAt = v.Last.Detail, v.Last.At
			if v.Kind != catalog.CheckPush {
				p.Latency = formatLatency(v.Last.Latency)
			}
			if !v.Last.CertExpiry.IsZero() {
				days := int(math.Floor(v.Last.CertExpiry.Sub(now).Hours() / 24))
				p.Cert = fmt.Sprintf("certificado hasta el %s (%d días)", v.Last.CertExpiry.UTC().Format("2006-01-02"), days)
				p.CertWarn = v.Last.CertExpiry.Sub(now) < status.DefaultParams.CertWarn
			}
		}
		var mine []store.CheckResult
		for _, res := range results {
			if res.Kind == string(v.Kind) && (v.Kind == catalog.CheckPush || !res.At.Before(start)) {
				mine = append(mine, res)
			}
		}
		if v.Kind == catalog.CheckPush {
			p.Uptime = pushUptime(mine, v.Deadline, start, now)
			p.Bars = pushBars(mine, v.Deadline, now, historySpan, historyBars)
		} else {
			p.Uptime = uptime(mine)
			p.Bars = bars(mine, now, historySpan, historyBars)
		}
		panels = append(panels, p)
	}
	return panels, rows
}

// checkLight es el semáforo de un check solo, con los mismos umbrales que
// el del servicio.
func checkLight(v CheckView) State {
	switch {
	case v.Results == 0:
		return StateUnknown
	case v.Failures == 0:
		return StateUp
	case v.Kind == catalog.CheckPush || v.Failures >= status.DefaultParams.FailThreshold:
		return StateDown
	default:
		return StateDegraded
	}
}

func kindLabel(k catalog.CheckKind) string {
	switch k {
	case catalog.CheckPush:
		return "Push"
	default:
		return strings.ToUpper(string(k))
	}
}

func formatLatency(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
}

// uptime es el porcentaje de resultados buenos, con coma decimal; vacío
// sin resultados. Nunca redondea a 100 si hubo alguna falla.
func uptime(results []store.CheckResult) string {
	if len(results) == 0 {
		return ""
	}
	ok := 0
	for _, r := range results {
		if r.OK {
			ok++
		}
	}
	return percent(float64(ok), float64(len(results)))
}

// bars reparte los resultados de [now-span, now) en n barras. Cada una
// muestra si todo salió bien ("up"), todo mal ("down"), una mezcla
// ("partial") o si no hubo resultados ("none").
func bars(results []store.CheckResult, now time.Time, span time.Duration, n int) []bar {
	start := now.Add(-span)
	width := span / time.Duration(n)
	type count struct{ ok, fail int }
	counts := make([]count, n)
	for _, r := range results {
		if r.At.Before(start) || !r.At.Before(now) {
			continue
		}
		i := int(r.At.Sub(start) / width)
		if r.OK {
			counts[i].ok++
		} else {
			counts[i].fail++
		}
	}
	out := make([]bar, n)
	for i, c := range counts {
		b := bar{From: start.Add(time.Duration(i) * width)}
		b.To = b.From.Add(width)
		switch {
		case c.ok+c.fail == 0:
			b.Class, b.Summary = "none", "sin resultados"
		case c.fail == 0:
			b.Class, b.Summary = "up", fmt.Sprintf("%d bien", c.ok)
		case c.ok == 0:
			b.Class, b.Summary = "down", plural(c.fail, "falla", "fallas")
		default:
			b.Class, b.Summary = "partial", fmt.Sprintf("%d bien, %s", c.ok, plural(c.fail, "falla", "fallas"))
		}
		out[i] = b
	}
	return out
}

// coverage mide un push en [from, to): cuánto tiempo hay datos (known) y
// cuánto de ese tiempo estuvo cubierto por una llamada a tiempo (covered).
// Cada llamada buena en t cubre [t, t+deadline). Los datos empiezan con el
// primer resultado: si es una falla ("no llegó"), el plazo que venció
// había empezado deadline antes. results va del más viejo al más nuevo.
func coverage(results []store.CheckResult, deadline time.Duration, from, to time.Time) (covered, known time.Duration) {
	if len(results) == 0 || !from.Before(to) {
		return 0, 0
	}
	first := results[0].At
	if !results[0].OK {
		first = first.Add(-deadline)
	}
	start := later(from, first)
	if !start.Before(to) {
		return 0, 0
	}
	known = to.Sub(start)

	// Unión de los tramos cubiertos, recortada a [start, to).
	var curFrom, curTo time.Time
	flush := func() {
		a, b := later(curFrom, start), curTo
		if b.After(to) {
			b = to
		}
		if a.Before(b) {
			covered += b.Sub(a)
		}
	}
	open := false
	for _, r := range results {
		if !r.OK {
			continue
		}
		a, b := r.At, r.At.Add(deadline)
		if open && !a.After(curTo) {
			curTo = later(curTo, b)
			continue
		}
		if open {
			flush()
		}
		curFrom, curTo, open = a, b, true
	}
	if open {
		flush()
	}
	return covered, known
}

// pushUptime es la parte del tiempo con datos de las últimas 24 horas en
// que el push estuvo al día. Contar resultados no sirve: cada intervalo sin
// llamada suma una falla, y una sola llamada buena cubre todo un intervalo.
func pushUptime(results []store.CheckResult, deadline time.Duration, from, to time.Time) string {
	covered, known := coverage(results, deadline, from, to)
	if known == 0 {
		return ""
	}
	return percent(float64(covered), float64(known))
}

// pushBars es bars para un push: cada barra según cuánto de su tramo
// estuvo cubierto.
func pushBars(results []store.CheckResult, deadline time.Duration, now time.Time, span time.Duration, n int) []bar {
	start := now.Add(-span)
	width := span / time.Duration(n)
	out := make([]bar, n)
	for i := range out {
		b := bar{From: start.Add(time.Duration(i) * width)}
		b.To = b.From.Add(width)
		covered, known := coverage(results, deadline, b.From, b.To)
		switch {
		case known == 0:
			b.Class, b.Summary = "none", "sin datos"
		case covered == known:
			b.Class, b.Summary = "up", "al día"
		case covered == 0:
			b.Class, b.Summary = "down", "atrasado"
		default:
			b.Class, b.Summary = "partial", "al día "+percent(float64(covered), float64(known))
		}
		out[i] = b
	}
	return out
}

// percent escribe a/b como porcentaje con coma decimal, sin redondear a
// 100 si no lo es.
func percent(a, b float64) string {
	if a >= b {
		return "100 %"
	}
	pct := math.Floor(a*1000/b) / 10
	return strings.Replace(fmt.Sprintf("%.1f %%", pct), ".", ",", 1)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// shownReason es el motivo de un evento para mostrar al lado de su estado:
// el "arriba" de un evento a Up repetiría la etiqueta ("Arriba: arriba").
func shownReason(reason string) string {
	if reason == "arriba" {
		return ""
	}
	return reason
}
