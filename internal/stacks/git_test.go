package stacks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// gitRepo convierte la raíz del Manager en un repo con todo commiteado. El
// .gitignore ignora los .env, como el de los stacks del server.
func gitRepo(t *testing.T, m *Manager) func(args ...string) string {
	t.Helper()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = m.root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Otro", "GIT_AUTHOR_EMAIL=otro@x", "GIT_COMMITTER_NAME=Otro", "GIT_COMMITTER_EMAIL=otro@x")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	must(t, os.WriteFile(filepath.Join(m.root, ".gitignore"), []byte(".env\n"), 0o644))
	git("add", "-A")
	git("commit", "-qm", "inicial")
	return git
}

func TestCommit(t *testing.T) {
	needTools(t)
	ctx := context.Background()

	t.Run("solo el archivo tocado", func(t *testing.T) {
		m, _ := setup(t)
		git := gitRepo(t, m)
		// Otro cambio en el índice y uno sin agregar: ninguno entra, y el
		// segundo sigue sin agregar (el helper recorta el espacio inicial
		// de " M", por eso se mira que no esté como "M  ", agregado).
		must(t, os.WriteFile(filepath.Join(m.root, "otro.txt"), []byte("x"), 0o644))
		git("add", "otro.txt")
		must(t, os.WriteFile(filepath.Join(m.root, ".gitignore"), []byte(".env\n# cambio\n"), 0o644))

		next := goodCompose + "    restart: always\n"
		res, err := m.Write(ctx, "demo", agent.FileCompose, []byte(next), hash([]byte(goodCompose)))
		must(t, err)
		if res.Commit == "" || res.Notice != "" {
			t.Fatalf("Commit = %q, Notice = %q", res.Commit, res.Notice)
		}
		if got := git("show", "--name-only", "--format=%an <%ae>|%s", "HEAD"); got != "Maestro <maestro@localhost>|demo: compose.yml editado desde Maestro\n\ndemo/compose.yml" {
			t.Errorf("commit = %q", got)
		}
		if got := git("rev-parse", "--short", "HEAD"); got != res.Commit {
			t.Errorf("Commit = %s, HEAD = %s", res.Commit, got)
		}
		if st := git("status", "--porcelain"); !strings.Contains(st, "A  otro.txt") || strings.Contains(st, "M  .gitignore") {
			t.Errorf("los otros cambios no quedaron como estaban: %q", st)
		}
	})

	t.Run(".env ignorado", func(t *testing.T) {
		m, _ := setup(t)
		git := gitRepo(t, m)
		head := git("rev-parse", "HEAD")
		res, err := m.Write(ctx, "demo", agent.FileEnv, []byte("X=2\n"), hash([]byte("X=1\n")))
		must(t, err)
		if res.Commit != "" || !strings.Contains(res.Notice, ".gitignore") {
			t.Errorf("Commit = %q, Notice = %q", res.Commit, res.Notice)
		}
		if git("rev-parse", "HEAD") != head {
			t.Error("hubo commit")
		}
	})

	t.Run("sin repo", func(t *testing.T) {
		m, _ := setup(t)
		res, err := m.Write(ctx, "demo", agent.FileEnv, []byte("X=2\n"), hash([]byte("X=1\n")))
		must(t, err)
		if res.Commit != "" || !strings.Contains(res.Notice, "no es parte de un repo") {
			t.Errorf("Commit = %q, Notice = %q", res.Commit, res.Notice)
		}
	})

	t.Run("hook que rechaza", func(t *testing.T) {
		m, dir := setup(t)
		gitRepo(t, m)
		hook := filepath.Join(m.root, ".git", "hooks", "pre-commit")
		must(t, os.WriteFile(hook, []byte("#!/bin/sh\necho 'no se permite' >&2\nexit 1\n"), 0o755))
		next := goodCompose + "    restart: always\n"
		res, err := m.Write(ctx, "demo", agent.FileCompose, []byte(next), hash([]byte(goodCompose)))
		must(t, err) // el archivo se guardó igual
		if res.Commit != "" || !strings.Contains(res.Notice, "no se permite") {
			t.Errorf("Commit = %q, Notice = %q", res.Commit, res.Notice)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(b) != next {
			t.Error("no se guardó")
		}
	})
}

func TestParseAuthor(t *testing.T) {
	for _, s := range []string{"", "sin mail", "<solo@mail>", "Nombre mail@x"} {
		if _, _, err := parseAuthor(s); err == nil {
			t.Errorf("parseAuthor(%q) aceptó", s)
		}
	}
	if n, e, err := parseAuthor("Roberto R <r@x.org>"); err != nil || n != "Roberto R" || e != "r@x.org" {
		t.Errorf("parseAuthor = %q, %q, %v", n, e, err)
	}
}
