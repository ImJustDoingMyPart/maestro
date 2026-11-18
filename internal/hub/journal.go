package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/journal"
)

// journalReader lee el journal de un host. Lo cumple *Poller.
type journalReader interface {
	Journal(ctx context.Context, host string, units []string, n int, after string) (journal.Page, error)
}

// journalPoll es cada cuánto se le piden al agent las líneas nuevas del
// journal: journalctl no se deja seguir por HTTP como los logs de Docker,
// así que se pregunta con el cursor de la última. Variable para los tests.
var journalPoll = 3 * time.Second

// streamJournal es streamLogs para un servicio de systemd: el journal de
// su unidad (un timer trae también el de su servicio) como los mismos
// eventos SSE que los logs de un contenedor, intercalado con los cambios de
// estado. El id de cada tanda es el cursor del journal: al reconectar, el
// navegador lo manda en Last-Event-ID y se sigue desde ahí.
func (s *server) streamJournal(w http.ResponseWriter, r *http.Request, svc catalog.Service) {
	jr, ok := s.logs.(journalReader)
	if !ok || svc.Host == "" {
		http.Error(w, "el servicio no tiene journal", http.StatusNotFound)
		return
	}
	units := journal.UnitsFor(svc.Unit)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}
	sse.raw("retry: 3000\n\n")
	if sse.flush() != nil {
		return
	}
	states, unsubscribe := s.mon.Subscribe(svc.ID)
	defer unsubscribe()
	tl := &timeline{sse: sse, states: states}

	cursor := r.Header.Get("Last-Event-ID")
	failing := false // ya se avisó que el agent no da el journal
	if cursor == "" {
		p, err := jr.Journal(ctx, svc.Host, units, initialTail, "")
		if err != nil {
			sse.event("notice", "No se pudo leer el journal: "+err.Error())
			failing = true
		}
		from := time.Now().Add(-historySpan)
		if len(p.Entries) > 0 {
			from = p.Entries[0].TS
		}
		events := s.stateEvents(ctx, svc.ID, from)
		for _, e := range p.Entries {
			for len(events) > 0 && !events[0].At.After(e.TS) {
				tl.send(events[0])
				events = events[1:]
			}
			sse.raw(journalEvent(e))
		}
		for _, e := range events {
			tl.send(e)
		}
		if p.Cursor != "" {
			cursor = p.Cursor
			sse.raw("id: " + cursor + "\n\n")
		}
		if sse.flush() != nil {
			return
		}
	}

	ticker := time.NewTicker(journalPoll)
	defer ticker.Stop()
	lastWrite := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-tl.states:
			tl.send(e)
		case <-ticker.C:
			p, err := jr.Journal(ctx, svc.Host, units, initialTail, cursor)
			switch {
			case ctx.Err() != nil:
				return
			case err != nil:
				if !failing {
					sse.event("notice", "No se pudo leer el journal: "+err.Error()+". Se sigue intentando.")
					failing = true
					break
				}
				if time.Since(lastWrite) < keepalive {
					continue
				}
				sse.comment("keepalive")
			case len(p.Entries) == 0:
				if time.Since(lastWrite) < keepalive {
					continue
				}
				sse.comment("keepalive")
			default:
				if failing {
					sse.event("notice", "El journal se lee de nuevo.")
					failing = false
				}
				for _, e := range p.Entries {
					sse.raw(journalEvent(e))
				}
				if p.Cursor != "" {
					cursor = p.Cursor
					sse.raw("id: " + cursor + "\n\n")
				}
			}
		}
		if sse.flush() != nil {
			return
		}
		lastWrite = time.Now()
	}
}

// journalEvent arma el evento SSE de una línea del journal, con el mismo
// data que una línea de Docker: c es quién la escribió (SYSLOG_IDENTIFIER)
// y lvl el nivel según la prioridad de syslog, que el visor usa en vez de
// adivinarlo por el texto.
func journalEvent(e journal.Entry) string {
	ev := logEvent{TS: e.TS, Stream: "stdout", Line: e.Message, Container: e.Ident}
	switch {
	case e.Priority <= 3:
		ev.Level, ev.Stream = "error", "stderr"
	case e.Priority == 4:
		ev.Level = "warn"
	}
	return "data: " + mustJSON(ev) + "\n\n"
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // solo tipos propios, sin canales ni funciones
	}
	return string(b)
}
