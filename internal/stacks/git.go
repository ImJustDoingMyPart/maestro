package stacks

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"path/filepath"
	"strings"
)

// DefaultAuthor es el autor de los commits si no se configura otro.
const DefaultAuthor = "Maestro <maestro@localhost>"

// parseAuthor separa "Nombre <mail>".
func parseAuthor(s string) (name, email string, err error) {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name == "" {
		return "", "", fmt.Errorf("autor de git inválido %q: tiene que ser \"Nombre <mail>\"", s)
	}
	return a.Name, a.Address, nil
}

// commit hace un commit solo de path, si su carpeta es parte de un repo y
// el archivo no está ignorado. Devuelve el hash corto, o un aviso de por qué
// no hubo commit. Un fallo de git no es un error: el archivo ya se guardó.
func (m *Manager) commit(ctx context.Context, project, path string) (hash, notice string) {
	dir, base := filepath.Split(path)
	git := func(args ...string) ([]byte, int, error) {
		// commit.gpgsign: el agent no tiene las llaves de nadie.
		full := append([]string{"-c", "commit.gpgsign=false"}, args...)
		return m.runEnv(ctx, dir, m.gitEnv, m.git, full...)
	}
	fail := func(what string, out []byte, err error) (string, string) {
		m.logger.Warn("git", "project", project, "file", path, "step", what, "err", err, "out", string(out))
		msg := strings.TrimSpace(string(out))
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return "", fmt.Sprintf("Se guardó, pero git %s falló: %s", what, msg)
	}

	if out, code, err := git("rev-parse", "--show-toplevel"); err != nil || code != 0 {
		if bytes.Contains(out, []byte("dubious ownership")) {
			return fail("rev-parse", out, err)
		}
		return "", "Se guardó sin commit: la carpeta no es parte de un repo de git."
	}
	switch out, code, err := git("check-ignore", "--quiet", "--", base); {
	case err != nil || code > 1:
		return fail("check-ignore", out, err)
	case code == 0:
		return "", fmt.Sprintf("Se guardó sin commit: %s está en el .gitignore del repo.", base)
	}
	if out, code, err := git("add", "--", base); err != nil || code != 0 {
		return fail("add", out, err)
	}
	msg := fmt.Sprintf("%s: %s editado desde Maestro", project, base)
	// Con una ruta, commit toma solo ese archivo (--only), aunque haya
	// otros cambios en el índice.
	if out, code, err := git("commit", "--quiet", "-m", msg, "--", base); err != nil || code != 0 {
		return fail("commit", out, err)
	}
	out, code, err := git("rev-parse", "--short", "HEAD")
	if err != nil || code != 0 {
		return fail("rev-parse", out, err)
	}
	return strings.TrimSpace(string(out)), ""
}

// gitEnvFor arma el entorno de git con el autor y el committer.
func gitEnvFor(author string) ([]string, error) {
	name, email, err := parseAuthor(author)
	if err != nil {
		return nil, err
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
		// Que git no pregunte nada ni abra un editor.
		"GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true",
	}, nil
}
