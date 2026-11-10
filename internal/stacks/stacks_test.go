package stacks

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

type fakeLister []agent.Container

func (f fakeLister) Containers(context.Context) ([]agent.Container, error) { return f, nil }

// needTools saltea el test si no están docker compose o git: los dos se usan
// de verdad (config valida sin hablar con el daemon).
func needTools(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("falta docker compose:", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("falta git")
	}
}

const goodCompose = `services:
  web:
    image: alpine
    environment:
      X: ${X:?falta X}
`

// setup arma una raíz con el stack "demo" (compose.yml y .env) y devuelve
// el Manager y la carpeta del stack.
func setup(t *testing.T, cs ...agent.Container) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	must(t, os.Mkdir(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(goodCompose), 0o640))
	must(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("X=1\n"), 0o600))
	m, err := New(Options{Root: root}, fakeLister(cs), slog.New(slog.DiscardHandler))
	must(t, err)
	return m, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func composeLabels(project, dir string, files ...string) agent.Container {
	return agent.Container{Labels: map[string]string{
		labelProject:     project,
		labelWorkingDir:  dir,
		labelConfigFiles: strings.Join(files, ","),
	}}
}

func TestLocate(t *testing.T) {
	m, dir := setup(t)
	outside := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "compose.yml"), []byte(goodCompose), 0o644))
	must(t, os.Mkdir(filepath.Join(m.root, "escape"), 0o755))
	must(t, os.Symlink(filepath.Join(outside, "compose.yml"), filepath.Join(m.root, "escape", "compose.yml")))

	tests := []struct {
		name    string
		cs      []agent.Container
		project string
		want    string // ruta del compose, o "" si tiene que fallar
		err     error
	}{
		{"por carpeta, sin contenedores", nil, "demo", filepath.Join(dir, "compose.yml"), nil},
		{"por labels", []agent.Container{composeLabels("demo", dir, filepath.Join(dir, "compose.yml"))}, "demo", filepath.Join(dir, "compose.yml"), nil},
		{"no existe", nil, "otro", "", agent.ErrStackNotFound},
		{"nombre inválido", nil, "../demo", "", agent.ErrStackNotFound},
		{"label fuera de la raíz", []agent.Container{composeLabels("x", outside, filepath.Join(outside, "compose.yml"))}, "x", "", agent.ErrOutsideRoot},
		{"symlink que escapa", nil, "escape", "", agent.ErrOutsideRoot},
		{"label que apunta a la nada", []agent.Container{composeLabels("demo", dir, "/no/existe/compose.yml")}, "demo", "", agent.ErrStackNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.containers = fakeLister(tt.cs)
			s, err := m.locate(context.Background(), tt.project)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, quería %v", err, tt.err)
				}
				return
			}
			must(t, err)
			if s.files[0] != tt.want {
				t.Errorf("compose = %s, quería %s", s.files[0], tt.want)
			}
		})
	}
}

func TestFiles(t *testing.T) {
	m, dir := setup(t)
	ctx := context.Background()
	got, err := m.Files(ctx, "demo")
	must(t, err)
	if len(got.Files) != 2 || got.Files[0].Content != goodCompose || got.Files[1].Content != "X=1\n" {
		t.Fatalf("Files = %+v", got)
	}
	if got.Files[0].Hash != hash([]byte(goodCompose)) {
		t.Errorf("hash = %s", got.Files[0].Hash)
	}

	must(t, os.Remove(filepath.Join(dir, ".env")))
	got, err = m.Files(ctx, "demo")
	must(t, err)
	if env := got.Files[1]; env.Exists || env.Hash != "" || env.Path != filepath.Join(dir, ".env") {
		t.Errorf(".env inexistente = %+v", env)
	}
}

func TestWrite(t *testing.T) {
	needTools(t)
	ctx := context.Background()

	t.Run("bien", func(t *testing.T) {
		m, dir := setup(t)
		next := strings.Replace(goodCompose, "alpine", "alpine:3", 1)
		res, err := m.Write(ctx, "demo", agent.FileCompose, []byte(next), hash([]byte(goodCompose)))
		must(t, err)
		b, _ := os.ReadFile(filepath.Join(dir, "compose.yml"))
		if string(b) != next {
			t.Errorf("contenido = %q", b)
		}
		if res.Hash != hash([]byte(next)) {
			t.Errorf("hash = %s", res.Hash)
		}
		if !strings.Contains(res.Diff, "+    image: alpine:3") || !strings.Contains(res.Diff, "a/compose.yml") {
			t.Errorf("diff = %q", res.Diff)
		}
		fi, _ := os.Stat(filepath.Join(dir, "compose.yml"))
		if fi.Mode().Perm() != 0o640 {
			t.Errorf("permisos = %v, quería 0640", fi.Mode().Perm())
		}
		noTemps(t, dir)
	})

	t.Run("inválido no se escribe", func(t *testing.T) {
		m, dir := setup(t)
		bad := "services:\n  web:\n    image: [\n"
		_, err := m.Write(ctx, "demo", agent.FileCompose, []byte(bad), hash([]byte(goodCompose)))
		var inv *agent.InvalidError
		if !errors.As(err, &inv) {
			t.Fatalf("err = %v, quería InvalidError", err)
		}
		if strings.Contains(inv.Output, ".maestro-") {
			t.Errorf("la salida nombra el temporal: %q", inv.Output)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "compose.yml"))
		if string(b) != goodCompose {
			t.Error("se escribió un compose inválido")
		}
		noTemps(t, dir)
	})

	t.Run(".env que rompe la interpolación", func(t *testing.T) {
		m, dir := setup(t)
		_, err := m.Write(ctx, "demo", agent.FileEnv, []byte("Y=2\n"), hash([]byte("X=1\n")))
		var inv *agent.InvalidError
		if !errors.As(err, &inv) || !strings.Contains(inv.Output, "falta X") {
			t.Fatalf("err = %v, quería InvalidError con 'falta X'", err)
		}
		noTemps(t, dir)
	})

	t.Run("conflicto", func(t *testing.T) {
		m, _ := setup(t)
		_, err := m.Write(ctx, "demo", agent.FileCompose, []byte(goodCompose+"\n"), hash([]byte("otra cosa")))
		if !errors.Is(err, agent.ErrConflict) {
			t.Errorf("err = %v, quería ErrConflict", err)
		}
	})

	t.Run(".env nuevo sale 0600", func(t *testing.T) {
		m, dir := setup(t)
		must(t, os.Remove(filepath.Join(dir, ".env")))
		must(t, os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services:\n  web:\n    image: alpine\n"), 0o644))
		res, err := m.Write(ctx, "demo", agent.FileEnv, []byte("A=1\n"), "")
		must(t, err)
		fi, err := os.Stat(filepath.Join(dir, ".env"))
		must(t, err)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("permisos = %v, quería 0600", fi.Mode().Perm())
		}
		if !strings.Contains(res.Diff, "/dev/null") || !strings.Contains(res.Diff, "+A=1") {
			t.Errorf("diff = %q", res.Diff)
		}
	})

	t.Run("symlink dentro de la raíz", func(t *testing.T) {
		m, dir := setup(t)
		real := filepath.Join(m.root, "compartido.yml")
		must(t, os.Rename(filepath.Join(dir, "compose.yml"), real))
		must(t, os.Symlink(real, filepath.Join(dir, "compose.yml")))
		next := goodCompose + "    restart: always\n"
		_, err := m.Write(ctx, "demo", agent.FileCompose, []byte(next), hash([]byte(goodCompose)))
		must(t, err)
		if fi, _ := os.Lstat(filepath.Join(dir, "compose.yml")); fi.Mode()&fs.ModeSymlink == 0 {
			t.Error("el symlink se reemplazó por un archivo")
		}
		if b, _ := os.ReadFile(real); string(b) != next {
			t.Errorf("destino del symlink = %q", b)
		}
	})

	t.Run("ocupado", func(t *testing.T) {
		m, _ := setup(t)
		m.acquire("demo", "up")
		_, err := m.Write(ctx, "demo", agent.FileCompose, []byte(goodCompose), hash([]byte(goodCompose)))
		if !errors.Is(err, agent.ErrBusy) {
			t.Errorf("err = %v, quería ErrBusy", err)
		}
	})

	t.Run("archivo que no se edita", func(t *testing.T) {
		m, _ := setup(t)
		_, err := m.Write(ctx, "demo", "../../etc/passwd", []byte("x"), "")
		if !errors.Is(err, agent.ErrBadFile) {
			t.Errorf("err = %v, quería ErrBadFile", err)
		}
	})
}

func TestCheck(t *testing.T) {
	needTools(t)
	m, dir := setup(t)
	ctx := context.Background()

	res, err := m.Check(ctx, "demo", agent.FileCompose, []byte(goodCompose))
	must(t, err)
	if res.Diff != "" {
		t.Errorf("sin cambios, diff = %q", res.Diff)
	}
	res, err = m.Check(ctx, "demo", agent.FileEnv, []byte("X=2\n"))
	must(t, err)
	if !strings.Contains(res.Diff, "-X=1") || !strings.Contains(res.Diff, "+X=2") {
		t.Errorf("diff = %q", res.Diff)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "X=1\n" {
		t.Error("Check escribió el archivo")
	}
	noTemps(t, dir)
}

// noTemps verifica que no quedaron temporales en la carpeta.
func noTemps(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".maestro-") {
			t.Errorf("quedó el temporal %s", e.Name())
		}
	}
}
