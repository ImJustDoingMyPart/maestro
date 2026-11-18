package stacks

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// listed es lo que se sabe de un stack de la carpeta, con la hora de los
// archivos de la que sale: si no cambiaron, no se vuelve a correr compose.
type listed struct {
	info agent.StackInfo
	key  string // rutas y horas de modificación del compose y el .env
}

// List da los stacks de la raíz: cada carpeta de primer nivel con un
// compose (compose.yaml, compose.yml, docker-compose.yaml o
// docker-compose.yml), con su nombre de proyecto y sus servicios según
// docker compose config. Los tenga corriendo o no: el hub cruza esto con
// los contenedores para saber cuáles están bajados.
//
// docker compose config tarda (lee el archivo, interpola el .env), así que
// el resultado de cada stack se guarda hasta que cambie la hora de sus
// archivos. Un stack cuyo compose no pasa sale con Err y sin servicios.
func (m *Manager) List(ctx context.Context) ([]agent.StackInfo, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return nil, err
	}
	var out []agent.StackInfo
	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(m.root, e.Name())
		file := ""
		for _, n := range composeNames {
			if fi, err := os.Stat(filepath.Join(dir, n)); err == nil && fi.Mode().IsRegular() {
				file = filepath.Join(dir, n)
				break
			}
		}
		if file == "" {
			continue
		}
		key := statKey(file) + "|" + statKey(filepath.Join(dir, ".env"))
		seen[dir] = true
		m.mu.Lock()
		c, ok := m.listCache[dir]
		m.mu.Unlock()
		if !ok || c.key != key {
			c = listed{info: m.describe(ctx, dir, file), key: key}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			m.mu.Lock()
			m.listCache[dir] = c
			m.mu.Unlock()
		}
		out = append(out, c.info)
	}
	m.mu.Lock()
	for dir := range m.listCache {
		if !seen[dir] {
			delete(m.listCache, dir)
		}
	}
	m.mu.Unlock()
	slices.SortFunc(out, func(a, b agent.StackInfo) int { return strings.Compare(a.Project, b.Project) })
	return out, nil
}

// statKey es la ruta con su hora de modificación y su tamaño; "" si no
// existe.
func statKey(p string) string {
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return ""
	}
	return p + "@" + fi.ModTime().UTC().Format(time.RFC3339Nano) + "#" + strconv.FormatInt(fi.Size(), 10)
}

// describe corre docker compose config --format json en el stack y saca
// el nombre del proyecto (el name: del archivo, o el de la carpeta como lo
// arma compose) y los servicios.
func (m *Manager) describe(ctx context.Context, dir, file string) agent.StackInfo {
	info := agent.StackInfo{Project: projectName(filepath.Base(dir)), Dir: dir}
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	out, code, err := m.run(ctx, dir, m.docker, "compose", "--ansi", "never", "-f", file, "--project-directory", dir, "config", "--format", "json", "--no-interpolate")
	if err != nil || code != 0 {
		msg := strings.TrimSpace(string(out))
		if err != nil {
			msg = err.Error()
		}
		info.Err = "docker compose config: " + lastLine(msg)
		return info
	}
	var cfg struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Labels map[string]string `json:"labels"`
		} `json:"services"`
	}
	// La salida puede traer avisos antes del JSON: se empieza en la
	// primera llave.
	if i := strings.IndexByte(string(out), '{'); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		info.Err = "docker compose config: salida inválida"
		return info
	}
	if cfg.Name != "" && agent.ValidProject(cfg.Name) {
		info.Project = cfg.Name
	}
	for name, svc := range cfg.Services {
		info.Services = append(info.Services, agent.StackService{Name: name, Labels: svc.Labels})
	}
	slices.SortFunc(info.Services, func(a, b agent.StackService) int { return strings.Compare(a.Name, b.Name) })
	return info
}

// projectName es el nombre de proyecto que compose le da a una carpeta: en
// minúsculas y solo con letras, números, "-" y "_".
func projectName(dir string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(dir) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "-_")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
