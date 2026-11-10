package stacks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// fakeDocker escribe un "docker" de mentira: imprime sus argumentos por
// stdout, una línea por stderr, espera lo que diga $FAKE_SLEEP, deja una
// marca en $FAKE_DONE y sale con $FAKE_EXIT.
func fakeDocker(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
echo "args: $*"
echo "Container demo-web-1 Started" >&2
sleep "${FAKE_SLEEP:-0}"
echo "fin"
[ -n "$FAKE_DONE" ] && touch "$FAKE_DONE"
exit "${FAKE_EXIT:-0}"
`
	must(t, os.WriteFile(p, []byte(script), 0o755))
	return p
}

func actionManager(t *testing.T) (*Manager, string) {
	t.Helper()
	m, dir := setup(t)
	m.docker = fakeDocker(t)
	return m, dir
}

func collect(t *testing.T, m *Manager, action string) ([]agent.ActionEvent, int) {
	t.Helper()
	events, err := m.Action(context.Background(), "demo", action)
	must(t, err)
	var got []agent.ActionEvent
	exit := -100
	for ev := range events {
		got = append(got, ev)
		if ev.Exit != nil {
			exit = *ev.Exit
		}
	}
	return got, exit
}

func TestAction(t *testing.T) {
	m, dir := actionManager(t)
	t.Setenv("FAKE_EXIT", "0")
	evs, exit := collect(t, m, agent.ActionUp)
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	var lines []string
	for _, ev := range evs {
		if ev.Line != "" {
			lines = append(lines, ev.Stream+": "+ev.Line)
		}
	}
	want := fmt.Sprintf("args: compose --ansi never --progress plain -p demo --project-directory %s -f %s up --detach", dir, filepath.Join(dir, "compose.yml"))
	if !strings.Contains(strings.Join(lines, "\n"), "stdout: "+want) {
		t.Errorf("no se corrió %q; salida:\n%s", want, strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "stderr: Container demo-web-1 Started") {
		t.Errorf("falta stderr; salida:\n%s", strings.Join(lines, "\n"))
	}
	if last := evs[len(evs)-1]; last.Exit == nil {
		t.Error("el último evento no lleva Exit")
	}

	t.Setenv("FAKE_EXIT", "17")
	if _, exit := collect(t, m, agent.ActionPull); exit != 17 {
		t.Errorf("exit = %d, quería 17", exit)
	}
}

func TestActionErrores(t *testing.T) {
	m, _ := actionManager(t)
	ctx := context.Background()
	if _, err := m.Action(ctx, "demo", "rm"); !errors.Is(err, agent.ErrBadAction) {
		t.Errorf("acción desconocida: %v", err)
	}
	if _, err := m.Action(ctx, "nada", agent.ActionUp); !errors.Is(err, agent.ErrStackNotFound) {
		t.Errorf("stack inexistente: %v", err)
	}
	// Un stack que no existe no tiene que quedar marcado como ocupado.
	if !m.acquire("nada", "x") {
		t.Error("el proyecto quedó ocupado después de un error")
	}

	m.docker = filepath.Join(t.TempDir(), "no-existe")
	evs, exit := collect(t, m, agent.ActionUp)
	if exit != -1 || evs[len(evs)-1].Error == "" {
		t.Errorf("binario inexistente: exit = %d, eventos %+v", exit, evs)
	}
}

// Una acción a la vez por proyecto; mientras corre, escribir también choca.
func TestActionOcupado(t *testing.T) {
	m, _ := actionManager(t)
	t.Setenv("FAKE_SLEEP", "0.3")
	ctx := context.Background()

	events, err := m.Action(ctx, "demo", agent.ActionUp)
	must(t, err)
	if _, err := m.Action(ctx, "demo", agent.ActionDown); !errors.Is(err, agent.ErrBusy) {
		t.Errorf("segunda acción: err = %v, quería ErrBusy", err)
	}
	if _, err := m.Write(ctx, "demo", agent.FileEnv, []byte("X=2\n"), hash([]byte("X=1\n"))); !errors.Is(err, agent.ErrBusy) {
		t.Errorf("escribir durante la acción: err = %v, quería ErrBusy", err)
	}
	files, err := m.Files(ctx, "demo")
	must(t, err)
	if !files.Busy {
		t.Error("Files no dice que está ocupado")
	}
	for range events {
	}
	// Al terminar se libera.
	if _, exit := collect(t, m, agent.ActionDown); exit != 0 {
		t.Errorf("después de terminar: exit = %d", exit)
	}
}

// Si quien lee se va, el comando sigue hasta el final y después se libera
// el proyecto.
func TestActionAbandonada(t *testing.T) {
	m, _ := actionManager(t)
	done := filepath.Join(t.TempDir(), "done")
	t.Setenv("FAKE_SLEEP", "0.3")
	t.Setenv("FAKE_DONE", done)

	events, err := m.Action(context.Background(), "demo", agent.ActionUp)
	must(t, err)
	for range events {
		break // se va con el primer evento
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil && m.acquire("demo", "x") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("el comando no terminó o el proyecto quedó ocupado")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
