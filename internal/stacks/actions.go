package stacks

import (
	"bufio"
	"context"
	"errors"
	"io"
	"iter"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// actionTimeout acota una acción: un pull de imágenes grandes con una
// conexión lenta tarda, pero no media hora.
const actionTimeout = 30 * time.Minute

// actionArgs es lo que va después de "docker compose <globales>".
var actionArgs = map[string][]string{
	agent.ActionUp:      {"up", "--detach"},
	agent.ActionDown:    {"down"},
	agent.ActionRestart: {"restart"},
	agent.ActionPull:    {"pull"},
}

// Action implementa agent.StackSource.Action.
//
// El comando no usa ctx (el del pedido): si el hub corta, el comando sigue
// hasta terminar y lo que falta de su salida va al log del agent. Para eso
// la salida pasa por un canal, y el iterador avisa con abandon cuando quien
// lo recorre se va.
func (m *Manager) Action(ctx context.Context, project, action string) (iter.Seq[agent.ActionEvent], error) {
	args, ok := actionArgs[action]
	if !ok {
		return nil, agent.ErrBadAction
	}
	if !m.acquire(project, action) {
		return nil, agent.ErrBusy
	}
	s, err := m.locate(ctx, project)
	if err != nil {
		m.release(project)
		return nil, err
	}

	full := []string{"compose", "--ansi", "never", "--progress", "plain", "-p", s.project, "--project-directory", s.dir}
	for _, f := range s.files {
		full = append(full, "-f", f)
	}
	full = append(full, args...)

	events := make(chan agent.ActionEvent, 64)
	abandon := make(chan struct{})
	go m.runAction(s, action, full, events, abandon)

	return func(yield func(agent.ActionEvent) bool) {
		defer close(abandon) // runAction deja de mandar y loguea el resto
		for ev := range events {
			if !yield(ev) {
				return
			}
		}
	}, nil
}

// runAction corre el comando y manda cada línea a events, que cierra al
// final. Si abandon se cierra, sigue igual pero las líneas van al log.
func (m *Manager) runAction(s stack, action string, args []string, events chan<- agent.ActionEvent, abandon <-chan struct{}) {
	defer m.release(s.project)
	defer close(events)
	start := time.Now()
	log := m.logger.With("project", s.project, "action", action)

	var gone bool // abandon ya se cerró
	send := func(ev agent.ActionEvent) {
		if !gone {
			select {
			case events <- ev:
				return
			case <-abandon:
				gone = true
			}
		}
		if ev.Line != "" {
			log.Info("salida sin cliente", "stream", ev.Stream, "line", ev.Line)
		}
	}
	finish := func(code int, errMsg string) {
		send(agent.ActionEvent{TS: time.Now(), Exit: &code, Error: errMsg})
		log.Info("acción terminada", "exit", code, "err", errMsg, "took", time.Since(start).Round(time.Millisecond))
	}

	// Sin el contexto del pedido, a propósito; solo el tope de tiempo.
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.docker, args...)
	cmd.Dir = s.dir
	cmd.Env = childEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		finish(-1, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		finish(-1, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		finish(-1, err.Error())
		return
	}
	log.Info("acción", "cmd", args)

	// Una goroutine por pipe junta las líneas en lines; send no es seguro
	// para dos goroutines (gone), así que las manda esta sola.
	lines := make(chan agent.ActionEvent)
	var wg sync.WaitGroup
	for name, r := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		wg.Go(func() { scanLines(r, name, lines) })
	}
	go func() { wg.Wait(); close(lines) }()
	for ev := range lines {
		send(ev)
	}

	// Wait recién después de leer todo: cierra los pipes al volver.
	err = cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		finish(0, "")
	case ctx.Err() != nil:
		finish(-1, "se pasó de "+actionTimeout.String()+" y se cortó")
	case errors.As(err, &ee):
		finish(ee.ExitCode(), "")
	default:
		finish(-1, err.Error())
	}
}

// scanLines manda cada línea de r a out.
func scanLines(r io.Reader, stream string, out chan<- agent.ActionEvent) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		// compose en modo plain deja espacios al final de cada renglón.
		out <- agent.ActionEvent{TS: time.Now(), Stream: stream, Line: strings.TrimRight(sc.Text(), " \t\r")}
	}
	// Una línea de más de 1 MiB corta el Scanner: se descarta el resto para
	// que el comando no se trabe con el pipe lleno.
	_, _ = io.Copy(io.Discard, r)
}
