// Package stacks lee, valida y escribe los archivos de los stacks de compose
// de un host, y corre docker compose sobre ellos. Lo usa el agent.
//
// Todo lo que toca está dentro de una carpeta raíz (--stacks-dir): una ruta
// que, resueltos los symlinks, cae afuera se rechaza.
package stacks

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// Labels que compose pone en cada contenedor.
const (
	labelProject     = "com.docker.compose.project"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
)

// composeNames son los nombres que compose busca solo, en su orden de
// preferencia. Se usan para un stack sin contenedores (bajado), que no
// tiene labels de dónde sacar la ruta.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// validateTimeout acota docker compose config, que no habla con el daemon.
const validateTimeout = 30 * time.Second

// ContainerLister da los contenedores del host, para encontrar el compose de
// un proyecto por sus labels. Lo cumple *docker.Client.
type ContainerLister interface {
	Containers(ctx context.Context) ([]agent.Container, error)
}

// Options configura un Manager.
type Options struct {
	Root   string // carpeta de los stacks; obligatoria
	Docker string // binario de docker; "docker" si está vacío
	Git    string // binario de git; "git" si está vacío
	// Author firma los commits ("Nombre <mail>"); DefaultAuthor si está
	// vacío.
	Author string
}

// Manager implementa agent.StackSource.
type Manager struct {
	root       string // absoluta y con los symlinks resueltos
	docker     string
	git        string
	gitEnv     []string // autor y committer de los commits
	containers ContainerLister
	logger     *slog.Logger

	mu   sync.Mutex
	busy map[string]string // proyecto → operación en curso
}

// New crea un Manager. La raíz tiene que existir.
func New(opts Options, containers ContainerLister, logger *slog.Logger) (*Manager, error) {
	if opts.Root == "" {
		return nil, errors.New("stacks: falta la carpeta de los stacks")
	}
	root, err := filepath.Abs(opts.Root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, fmt.Errorf("stacks: carpeta %s: %w", opts.Root, err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("stacks: %s no es una carpeta", root)
	}
	gitEnv, err := gitEnvFor(cmp.Or(opts.Author, DefaultAuthor))
	if err != nil {
		return nil, fmt.Errorf("stacks: %w", err)
	}
	m := &Manager{
		root:       root,
		docker:     cmp.Or(opts.Docker, "docker"),
		git:        cmp.Or(opts.Git, "git"),
		gitEnv:     gitEnv,
		containers: containers,
		logger:     logger,
		busy:       make(map[string]string),
	}
	return m, nil
}

// stack es dónde vive un proyecto, con las rutas ya resueltas y verificadas.
type stack struct {
	project string
	dir     string   // carpeta del proyecto (--project-directory)
	files   []string // archivos de compose; el primero es el que se edita
	env     string   // .env del proyecto, exista o no
}

// path devuelve la ruta del archivo lógico name.
func (s stack) path(name string) (string, error) {
	switch name {
	case agent.FileCompose:
		return s.files[0], nil
	case agent.FileEnv:
		return s.env, nil
	}
	return "", agent.ErrBadFile
}

// locate encuentra el stack de un proyecto: primero por los labels de sus
// contenedores, que dicen exactamente qué archivos usó compose; si no tiene
// contenedores (está bajado), por <raíz>/<proyecto>/compose.yml.
func (m *Manager) locate(ctx context.Context, project string) (stack, error) {
	if !agent.ValidProject(project) {
		return stack{}, agent.ErrStackNotFound
	}
	var files []string
	var dir string
	cs, err := m.containers.Containers(ctx)
	if err != nil {
		return stack{}, err
	}
	for _, c := range cs {
		if c.Labels[labelProject] != project {
			continue
		}
		for f := range strings.SplitSeq(c.Labels[labelConfigFiles], ",") {
			if f = strings.TrimSpace(f); f != "" {
				files = append(files, f)
			}
		}
		dir = c.Labels[labelWorkingDir]
		break
	}
	if len(files) == 0 {
		for _, name := range composeNames {
			p := filepath.Join(m.root, project, name)
			if _, err := os.Lstat(p); err == nil {
				files = []string{p}
				break
			}
		}
	}
	if len(files) == 0 {
		return stack{}, agent.ErrStackNotFound
	}
	if dir == "" {
		dir = filepath.Dir(files[0])
	}

	s := stack{project: project}
	if s.dir, err = m.resolve(dir); err != nil {
		return stack{}, err
	}
	for _, f := range files {
		r, err := m.resolve(f)
		if err != nil {
			return stack{}, err
		}
		s.files = append(s.files, r)
	}
	// El .env puede no existir: entonces se resuelve la carpeta, que sí.
	env := filepath.Join(s.dir, ".env")
	if _, err := os.Lstat(env); err == nil {
		if s.env, err = m.resolve(env); err != nil {
			return stack{}, err
		}
	} else {
		s.env = env
	}
	return s, nil
}

// resolve resuelve los symlinks de p y verifica que caiga dentro de la
// raíz. Un symlink roto o un archivo inexistente es ErrStackNotFound: desde
// afuera no hay diferencia.
func (m *Manager) resolve(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: ruta relativa %q", agent.ErrStackNotFound, p)
	}
	r, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s no existe (¿la carpeta de los stacks está montada en la misma ruta que en el host?)", agent.ErrStackNotFound, p)
	}
	if err != nil {
		return "", err
	}
	if !within(m.root, r) {
		return "", fmt.Errorf("%w: %s", agent.ErrOutsideRoot, p)
	}
	return r, nil
}

// within dice si p está dentro de root (las dos absolutas y limpias).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Files lee el compose y el .env de un proyecto.
func (m *Manager) Files(ctx context.Context, project string) (agent.StackFiles, error) {
	s, err := m.locate(ctx, project)
	if err != nil {
		return agent.StackFiles{}, err
	}
	out := agent.StackFiles{Project: project, Dir: s.dir}
	for _, name := range []string{agent.FileCompose, agent.FileEnv} {
		p, _ := s.path(name)
		f := agent.StackFile{Name: name, Path: p}
		b, err := os.ReadFile(p)
		switch {
		case err == nil:
			f.Exists, f.Content, f.Hash = true, string(b), hash(b)
		case errors.Is(err, fs.ErrNotExist) && name == agent.FileEnv:
		default:
			return agent.StackFiles{}, err
		}
		out.Files = append(out.Files, f)
	}
	m.mu.Lock()
	out.Busy = m.busy[project] != ""
	m.mu.Unlock()
	return out, nil
}

// Check valida content como el archivo name del proyecto y devuelve el diff
// contra lo que hay en el disco, sin tocar nada.
func (m *Manager) Check(ctx context.Context, project, name string, content []byte) (agent.CheckResult, error) {
	s, err := m.locate(ctx, project)
	if err != nil {
		return agent.CheckResult{}, err
	}
	target, err := s.path(name)
	if err != nil {
		return agent.CheckResult{}, err
	}
	tmp, err := m.stage(target, content)
	if err != nil {
		return agent.CheckResult{}, err
	}
	defer os.Remove(tmp)
	warnings, err := m.validate(ctx, s, name, tmp)
	if err != nil {
		return agent.CheckResult{}, err
	}
	diff, err := m.diff(ctx, target, tmp)
	return agent.CheckResult{Diff: diff, Warnings: warnings}, err
}

// Write valida content y, si pasa, reemplaza el archivo name del proyecto.
// hash es el que se leyó con Files ("" si el archivo no existía): si el
// archivo cambió desde entonces, ErrConflict.
func (m *Manager) Write(ctx context.Context, project, name string, content []byte, wantHash string) (agent.WriteResult, error) {
	if !m.acquire(project, "escribir "+name) {
		return agent.WriteResult{}, agent.ErrBusy
	}
	defer m.release(project)

	s, err := m.locate(ctx, project)
	if err != nil {
		return agent.WriteResult{}, err
	}
	target, err := s.path(name)
	if err != nil {
		return agent.WriteResult{}, err
	}
	cur, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return agent.WriteResult{}, err
	}
	curHash := ""
	if err == nil {
		curHash = hash(cur)
	}
	if curHash != wantHash {
		return agent.WriteResult{}, agent.ErrConflict
	}
	newHash := hash(content)
	if newHash == curHash {
		return agent.WriteResult{Hash: newHash}, nil // nada que escribir
	}

	tmp, err := m.stage(target, content)
	if err != nil {
		return agent.WriteResult{}, err
	}
	defer os.Remove(tmp) // después del rename ya no existe y esto no hace nada
	// Los avisos ya los vio quien guarda: el navegador valida con Check
	// antes de mostrar el diff.
	if _, err := m.validate(ctx, s, name, tmp); err != nil {
		return agent.WriteResult{}, err
	}
	diff, err := m.diff(ctx, target, tmp)
	if err != nil {
		return agent.WriteResult{}, err
	}
	if err := commitFile(tmp, target); err != nil {
		return agent.WriteResult{}, err
	}
	m.logger.Info("archivo escrito", "project", project, "file", target)
	res := agent.WriteResult{Hash: newHash, Diff: diff}
	res.Commit, res.Notice = m.commit(ctx, project, target)
	return res, nil
}

// stage escribe content en un archivo temporal al lado de target, con los
// permisos y el dueño de target (0600 si target no existe: el .env lleva
// secretos). Al lado y no en /tmp por dos razones: compose resuelve las
// rutas relativas del archivo desde su carpeta, y el rename final es
// atómico solo dentro del mismo sistema de archivos.
func (m *Manager) stage(target string, content []byte) (string, error) {
	dir, base := filepath.Split(target)
	f, err := os.CreateTemp(dir, ".maestro-*-"+base)
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}

	mode := fs.FileMode(0o600)
	fi, statErr := os.Stat(target)
	if statErr == nil {
		mode = fi.Mode().Perm()
	}
	if _, err := f.Write(content); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if statErr == nil {
		if err := keepOwner(f, fi); err != nil {
			return fail(fmt.Errorf("%s: %w", target, err))
		}
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// keepOwner le pone a f el dueño y el grupo de fi, si son otros. Si el
// agent no puede (no es root ni el dueño), falla en vez de dejar el archivo
// con otro dueño.
func keepOwner(f *os.File, fi fs.FileInfo) error {
	want, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	got, err := f.Stat()
	if err != nil {
		return err
	}
	have := got.Sys().(*syscall.Stat_t)
	if have.Uid == want.Uid && have.Gid == want.Gid {
		return nil
	}
	if err := f.Chown(int(want.Uid), int(want.Gid)); err != nil {
		return fmt.Errorf("el archivo es de %d:%d y el agent no puede crearlo así (corre como %d:%d): %w",
			want.Uid, want.Gid, have.Uid, have.Gid, err)
	}
	return nil
}

// commitFile reemplaza target por tmp con un rename, que es atómico: quien
// lea target ve el archivo viejo o el nuevo, nunca uno a medias. Después
// sincroniza la carpeta para que el rename sobreviva a un corte de luz.
func commitFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// validate corre docker compose config con tmp en el lugar del archivo name.
// Si el archivo pasa, devuelve los avisos que haya escrito compose; si no,
// un *agent.InvalidError con lo que dijo.
func (m *Manager) validate(ctx context.Context, s stack, name, tmp string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()

	args := []string{"compose", "--ansi", "never", "-p", s.project, "--project-directory", s.dir}
	switch name {
	case agent.FileCompose:
		args = append(args, "-f", tmp)
		for _, f := range s.files[1:] {
			args = append(args, "-f", f)
		}
	case agent.FileEnv:
		for _, f := range s.files {
			args = append(args, "-f", f)
		}
		args = append(args, "--env-file", tmp)
	}
	args = append(args, "config", "--quiet")

	out, code, err := m.run(ctx, s.dir, m.docker, args...)
	if err != nil {
		return nil, fmt.Errorf("docker compose config: %w", err)
	}
	// Los mensajes nombran el temporal: se cambia por el archivo de verdad.
	target, _ := s.path(name)
	untemp := func(msg string) string {
		msg = strings.ReplaceAll(msg, tmp, target)
		return strings.ReplaceAll(msg, filepath.Base(tmp), filepath.Base(target))
	}
	if code != 0 {
		return nil, &agent.InvalidError{Output: untemp(strings.TrimSpace(string(out)))}
	}
	var warnings []string
	for l := range strings.Lines(string(out)) {
		if w := warningText(l); w != "" {
			warnings = append(warnings, untemp(w))
		}
	}
	return warnings, nil
}

// logrusMsg es el mensaje de un renglón de log de compose, que escribe los
// avisos así: time="..." level=warning msg="The \"X\" variable is not set...".
var logrusMsg = regexp.MustCompile(`\bmsg="((?:[^"\\]|\\.)*)"`)

// warningText saca el mensaje de un renglón de aviso de compose. Un renglón
// que no tiene la forma de log se devuelve tal cual, sin espacios de más.
func warningText(line string) string {
	line = strings.TrimSpace(line)
	if m := logrusMsg.FindStringSubmatch(line); m != nil {
		if msg, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
			return msg
		}
		return m[1]
	}
	return line
}

// diff compara target (o nada, si no existe) con tmp usando git diff
// --no-index, que no necesita un repo. Los nombres del encabezado quedan
// como los de target.
func (m *Manager) diff(ctx context.Context, target, tmp string) (string, error) {
	dir, base := filepath.Split(target)
	old := base
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		old = os.DevNull
	}
	out, code, err := m.run(ctx, dir, m.git, "diff", "--no-index", "--no-color", "--no-ext-diff", "--", old, filepath.Base(tmp))
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	// Con --no-index, 1 es "hay diferencias" y 0 "son iguales".
	if code > 1 {
		return "", fmt.Errorf("git diff: salió con %d: %s", code, bytes.TrimSpace(out))
	}
	return strings.ReplaceAll(string(out), filepath.Base(tmp), base), nil
}

// run corre un comando en dir y devuelve la salida combinada y el código de
// salida. err es solo para cuando no se pudo correr (binario inexistente,
// contexto vencido), no para un código distinto de cero.
func (m *Manager) run(ctx context.Context, dir, bin string, args ...string) ([]byte, int, error) {
	return m.runEnv(ctx, dir, nil, bin, args...)
}

// runEnv es run con variables de entorno de más.
func (m *Manager) runEnv(ctx context.Context, dir string, env []string, bin string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(), env...)
	out, err := cmd.CombinedOutput()
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ctx.Err() == nil {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// acquire marca el proyecto como ocupado con op. Devuelve false si ya lo
// estaba.
func (m *Manager) acquire(project, op string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy[project] != "" {
		return false
	}
	m.busy[project] = op
	return true
}

func (m *Manager) release(project string) {
	m.mu.Lock()
	delete(m.busy, project)
	m.mu.Unlock()
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// childEnv es el entorno del agent sin las variables MAESTRO_*: compose
// interpola con el entorno de quien lo corre, y el token del agent no tiene
// por qué quedar a su alcance.
func childEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "MAESTRO_") })
}
