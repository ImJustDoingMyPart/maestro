package hub

import (
	"cmp"
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/loglevel"
	"github.com/ImJustDoingMyPart/maestro/internal/redact"
	"github.com/ImJustDoingMyPart/maestro/internal/report"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

const (
	// reportTail es cuántas líneas de log entran por contenedor.
	reportTail = 200
	// reportFailures es cuántas fallas recientes de cada check entran.
	reportFailures = 10
	// reportTimeout acota cuánto se espera al agent (logs y archivos).
	reportTimeout = 15 * time.Second
)

// reportSuffix termina la ruta del informe: /api/services/<id>/report. El ID
// de compose lleva "/", así que la ruta se toma entera ({rest...}) y se le
// saca el sufijo: el informe de "x/report" es /api/services/x/report/report.
const reportSuffix = "/report"

// serviceReport atiende GET /api/services/{rest...}. Pide sesión o el token
// de la API (Authorization: Bearer), así un agente lo puede pedir con curl.
// Sin ninguno de los dos responde 401, no redirige al login.
func (s *server) serviceReport(w http.ResponseWriter, r *http.Request) {
	if !s.apiAuthorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="maestro"`)
		http.Error(w, "hace falta sesión o el token de la API (Authorization: Bearer)", http.StatusUnauthorized)
		return
	}
	id, ok := strings.CutSuffix(r.PathValue("rest"), reportSuffix)
	if !ok || id == "" {
		http.NotFound(w, r)
		return
	}
	svc, found := s.lookup(r.Context(), id)
	if !found {
		http.Error(w, "servicio inexistente", http.StatusNotFound)
		return
	}
	md := report.Render(s.buildReport(r.Context(), svc, time.Now()))
	// text/plain y no text/markdown: el navegador lo muestra en vez de
	// bajarlo, y para curl da lo mismo.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(md))
}

// apiAuthorized acepta una sesión válida o el token de la API.
func (s *server) apiAuthorized(r *http.Request) bool {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.apiToken != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiToken)) == 1 {
			return true
		}
		s.logger.Warn("token de API inválido", "remote", r.RemoteAddr,
			"forwarded_for", r.Header.Get("X-Forwarded-For"))
		return false
	}
	return s.sessions.LoggedIn(r)
}

// buildReport junta los datos del informe y los censura. Todo texto que
// viene de afuera (el compose, el .env, los logs, los motivos, los
// resultados de los checks, las URLs) pasa por el mismo Redactor, que
// primero aprende los valores secretos del .env.
func (s *server) buildReport(ctx context.Context, svc catalog.Service, now time.Time) report.Report {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	var red redact.Redactor

	rep := report.Report{
		Generated:   now,
		ID:          svc.ID,
		Name:        cmp.Or(svc.Name, svc.ID),
		Group:       svc.Group,
		Host:        svc.Host,
		WindowHours: int(historySpan / time.Hour),
		LogsTail:    reportTail,
	}

	// Los archivos primero: el .env le enseña al Redactor qué tapar en lo
	// demás.
	if svc.Source == catalog.SourceCompose {
		rep.Files, rep.FilesNote = s.reportFiles(ctx, svc, &red)
	}

	d := detail(svc, s.mon.Status(svc.ID))
	rep.Description = red.Text(svc.Description)
	rep.URL = red.Text(svc.URL)
	rep.Source = d.Source
	rep.State = d.State.Label()
	for _, reason := range d.Reasons {
		rep.Reasons = append(rep.Reasons, oneLine(red.Text(reason)))
	}
	for _, c := range svc.Containers {
		rc := report.Container{
			Name: c.Name, Image: c.Image, State: c.State, Health: c.Health,
			StartedAt: c.StartedAt, Restarts: c.Restarts, ExitCode: c.LastExitCode,
		}
		if rc.ExitCode == nil && c.State != "running" && c.State != "created" {
			code := c.ExitCode
			rc.ExitCode = &code
		}
		rep.Containers = append(rep.Containers, rc)
	}

	rep.Checks, rep.Events = s.reportHistory(ctx, svc, now, &red)
	for i := len(rep.Events) - 1; i >= 0; i-- {
		if e := rep.Events[i]; e.To == rep.State {
			rep.StateSince = e.At
			break
		}
	}

	if d.LogsURL == "" {
		rep.LogsNote = cmp.Or(d.NoLogs, "El servicio no tiene contenedores: no hay logs que leer.")
	} else {
		rep.Logs, rep.LogsNote = s.reportLogs(ctx, svc.Containers, &red)
	}
	return rep
}

// reportFiles lee el compose y el .env del stack y los censura (el .env
// primero).
func (s *server) reportFiles(ctx context.Context, svc catalog.Service, red *redact.Redactor) ([]report.File, string) {
	files, err := s.stacks.StackFiles(ctx, svc.Host, svc.Project)
	switch {
	case errors.Is(err, errNoAgent):
		return nil, "No hay un agent configurado para el host " + svc.Host + "."
	case errors.Is(err, agent.ErrStackNotFound):
		return nil, "El agent no encuentra la carpeta del stack " + svc.Project + "."
	case err != nil:
		s.logger.Warn("informe: leer stack", "host", svc.Host, "project", svc.Project, "err", err)
		return nil, "No se pudieron leer los archivos del stack (ver el log del hub)."
	}
	var compose, env *agent.StackFile
	for i := range files.Files {
		switch files.Files[i].Name {
		case agent.FileCompose:
			compose = &files.Files[i]
		case agent.FileEnv:
			env = &files.Files[i]
		}
	}
	var envOut *report.File
	if env != nil {
		envOut = &report.File{Name: cmp.Or(path.Base(env.Path), ".env"), Path: env.Path, Exists: env.Exists, Lang: "dotenv",
			Content: red.Env(env.Content)}
	}
	var out []report.File
	if compose != nil {
		out = append(out, report.File{Name: cmp.Or(path.Base(compose.Path), "compose.yml"), Path: compose.Path, Exists: compose.Exists, Lang: "yaml",
			Content: red.Compose(compose.Content)})
	}
	if envOut != nil {
		out = append(out, *envOut)
	}
	return out, ""
}

// reportHistory arma los checks y los cambios de estado de la ventana.
func (s *server) reportHistory(ctx context.Context, svc catalog.Service, now time.Time, red *redact.Redactor) ([]report.Check, []report.Event) {
	start := now.Add(-historySpan)
	views := s.mon.Checks(svc.ID)
	lookback := time.Duration(0)
	for _, v := range views {
		lookback = max(lookback, v.Deadline)
	}
	results, events, err := s.mon.History(ctx, svc.ID, start.Add(-lookback))
	if err != nil {
		s.logger.Error("informe: leer historial", "service", svc.ID, "err", err)
	}

	var checks []report.Check
	for _, v := range views {
		c := report.Check{Kind: kindLabel(v.Kind), Target: red.Text(v.Target)}
		if v.Results > 0 {
			c.Last = oneLine(red.Text(lastResult(v)))
			c.LastAt = v.Last.At
			if v.Kind != catalog.CheckPush {
				c.Latency = formatLatency(v.Last.Latency)
			}
			c.CertExpiry = v.Last.CertExpiry
		}
		var mine []store.CheckResult
		for _, res := range results {
			if res.Kind == string(v.Kind) && (v.Kind == catalog.CheckPush || !res.At.Before(start)) {
				mine = append(mine, res)
			}
		}
		if v.Kind == catalog.CheckPush {
			c.Uptime = pushUptime(mine, v.Deadline, start, now)
		} else {
			c.Uptime = uptime(mine)
		}
		for i := len(mine) - 1; i >= 0 && len(c.Failures) < reportFailures; i-- {
			if res := mine[i]; !res.OK && !res.At.Before(start) {
				c.Failures = append(c.Failures, report.Failure{At: res.At, Detail: oneLine(red.Text(res.Detail))})
			}
		}
		checks = append(checks, c)
	}

	var out []report.Event
	for _, e := range events {
		if e.At.Before(start) {
			continue
		}
		out = append(out, report.Event{
			At: e.At, From: State(e.From).Label(), To: State(e.To).Label(),
			Reason: oneLine(red.Text(shownReason(e.Reason))),
		})
	}
	return checks, out
}

// lastResult es el último resultado de un check en palabras.
func lastResult(v CheckView) string {
	if v.Last.OK {
		return cmp.Or("ok · "+v.Last.Detail, "ok")
	}
	return "falla: " + v.Last.Detail
}

// reportLogs lee las últimas reportTail líneas de cada contenedor, las
// intercala por hora y las censura. Si ningún contenedor respondió, la nota
// dice por qué.
func (s *server) reportLogs(ctx context.Context, cs []catalog.Container, red *redact.Redactor) ([]report.LogLine, string) {
	type raw struct {
		at        time.Time
		container string
		text      string
	}
	var (
		mu    sync.Mutex
		all   []raw
		fails []string
		wg    sync.WaitGroup
	)
	multi := len(cs) > 1
	for _, c := range cs {
		wg.Go(func() {
			defer s.recoverAs(func(err error) {
				mu.Lock()
				fails = append(fails, c.Name+": "+err.Error())
				mu.Unlock()
			})
			var mine []raw
			err := s.readLogs(ctx, c, agent.LogOptions{Tail: reportTail}, func(l agent.LogLine) bool {
				name := ""
				if multi {
					name = c.Name
				}
				mine = append(mine, raw{at: l.TS, container: name, text: l.Line})
				return true
			})
			mu.Lock()
			defer mu.Unlock()
			all = append(all, mine...)
			if err != nil {
				fails = append(fails, c.Name+": "+err.Error())
			}
		})
	}
	wg.Wait()
	if len(all) == 0 && len(fails) > 0 {
		slices.Sort(fails)
		return nil, "No se pudieron leer los logs: " + strings.Join(fails, "; ") + "."
	}
	slices.SortStableFunc(all, func(a, b raw) int { return a.at.Compare(b.at) })

	out := make([]report.LogLine, 0, len(all))
	for _, l := range all {
		text := loglevel.StripANSI(strings.TrimRight(l.text, "\r\n"))
		out = append(out, report.LogLine{
			At: l.at, Container: l.container,
			// El nivel sale de la línea sin censurar: la censura no toca
			// las palabras de nivel, pero así no depende de eso.
			Level: loglevel.Detect(text),
			Text:  red.Text(text),
		})
	}
	return out, ""
}

// oneLine junta un texto de varias líneas en una, para las listas del
// informe.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
