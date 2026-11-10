// Command npmfetch baja paquetes de npm sin Node, para que esbuild los
// encuentre en node_modules.
//
// Las versiones están fijas en un lockfile propio, una por renglón:
//
//	@codemirror/view 6.43.15 sha512-<base64>
//
// Cada tarball se verifica contra su integrity (el mismo sha512 que guarda
// package-lock.json) antes de abrirlo. No resuelve rangos de versiones: el
// lockfile lista todos los paquetes, también los transitivos, y npmfetch
// avisa si a alguno le falta una dependencia.
//
// Uso:
//
//	npmfetch -lock npm.lock -dir node_modules            sincroniza
//	npmfetch -lock npm.lock -add @lezer/yaml@1.0.4 ...   agrega o actualiza
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const registry = "https://registry.npmjs.org"

// maxTarball acota lo que se baja: el más grande de CodeMirror ronda 300 KiB.
const maxTarball = 20 << 20

// marker es el archivo que npmfetch deja en cada paquete con su integrity:
// si coincide con el del lockfile, el paquete está al día.
const marker = ".npmfetch"

type pkg struct {
	Name, Version, Integrity string
}

func (p pkg) tarballURL() string {
	base := p.Name[strings.LastIndex(p.Name, "/")+1:]
	return registry + "/" + p.Name + "/-/" + base + "-" + p.Version + ".tgz"
}

func main() {
	lock := flag.String("lock", "npm.lock", "lockfile")
	dir := flag.String("dir", "node_modules", "carpeta destino")
	add := flag.Bool("add", false, "agregar o actualizar los paquetes nombre@versión de los argumentos")
	flag.Parse()

	client := &http.Client{Timeout: time.Minute}
	var err error
	if *add {
		err = runAdd(client, *lock, flag.Args())
	} else {
		err = runSync(client, *lock, *dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "npmfetch:", err)
		os.Exit(1)
	}
}

func readLock(file string) ([]pkg, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pkg
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 || !strings.HasPrefix(f[2], "sha512-") || !validName(f[0]) {
			return nil, fmt.Errorf("%s:%d: renglón inválido", file, n)
		}
		out = append(out, pkg{f[0], f[1], f[2]})
	}
	return out, sc.Err()
}

// validName acepta "nombre" o "@scope/nombre", sin nada que pueda salir de
// node_modules.
func validName(n string) bool {
	parts := strings.Split(n, "/")
	if len(parts) == 2 && !strings.HasPrefix(parts[0], "@") || len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || p == "@" || strings.ContainsAny(p, `\`) {
			return false
		}
	}
	return true
}

func writeLock(file string, pkgs []pkg) error {
	slices.SortFunc(pkgs, func(a, b pkg) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	b.WriteString("# Paquetes de npm para el bundle del editor. Lo mantiene internal/npmfetch:\n")
	b.WriteString("#   go run ./internal/npmfetch -lock web/npm.lock -add <paquete>@<versión>\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "%s %s %s\n", p.Name, p.Version, p.Integrity)
	}
	return os.WriteFile(file, []byte(b.String()), 0o644)
}

// runAdd pide al registry el integrity de cada nombre@versión y lo guarda.
func runAdd(client *http.Client, lock string, args []string) error {
	pkgs, err := readLock(lock)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	for _, a := range args {
		at := strings.LastIndex(a, "@")
		if at <= 0 {
			return fmt.Errorf("%q: falta la versión (nombre@versión)", a)
		}
		name, version := a[:at], a[at+1:]
		if !validName(name) {
			return fmt.Errorf("%q: nombre inválido", name)
		}
		var meta struct {
			Dist struct {
				Tarball   string `json:"tarball"`
				Integrity string `json:"integrity"`
			} `json:"dist"`
		}
		if err := getJSON(client, registry+"/"+name+"/"+version, &meta); err != nil {
			return err
		}
		p := pkg{name, version, meta.Dist.Integrity}
		if !strings.HasPrefix(p.Integrity, "sha512-") {
			return fmt.Errorf("%s: el registry no da un integrity sha512", a)
		}
		if meta.Dist.Tarball != p.tarballURL() {
			return fmt.Errorf("%s: el tarball está en %s, no en %s", a, meta.Dist.Tarball, p.tarballURL())
		}
		pkgs = slices.DeleteFunc(pkgs, func(q pkg) bool { return q.Name == name })
		pkgs = append(pkgs, p)
		fmt.Printf("%s %s\n", name, version)
	}
	return writeLock(lock, pkgs)
}

// runSync deja en dir exactamente las versiones del lockfile.
func runSync(client *http.Client, lock, dir string) error {
	pkgs, err := readLock(lock)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		dest := filepath.Join(dir, filepath.FromSlash(p.Name))
		if b, err := os.ReadFile(filepath.Join(dest, marker)); err == nil && strings.TrimSpace(string(b)) == p.Integrity {
			continue
		}
		if err := fetch(client, p, dest); err != nil {
			return fmt.Errorf("%s@%s: %w", p.Name, p.Version, err)
		}
		fmt.Printf("npmfetch: %s@%s\n", p.Name, p.Version)
	}
	return checkDeps(pkgs, dir)
}

// fetch baja, verifica y extrae un paquete en dest, reemplazando lo que
// hubiera.
func fetch(client *http.Client, p pkg, dest string) error {
	res, err := client.Get(p.tarballURL())
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", p.tarballURL(), res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxTarball+1))
	if err != nil {
		return err
	}
	if len(data) > maxTarball {
		return errors.New("el tarball pasa de 20 MiB")
	}
	sum := sha512.Sum512(data)
	if got := "sha512-" + base64.StdEncoding.EncodeToString(sum[:]); got != p.Integrity {
		return fmt.Errorf("integrity distinto: el lockfile dice %s y el tarball da %s", p.Integrity, got)
	}

	// Se extrae al lado y se cambia de lugar al final: si algo falla a la
	// mitad, no queda un paquete a medias.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".npmfetch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extract(data, tmp); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, marker), []byte(p.Integrity+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// extract abre un .tgz de npm en dir. Los archivos vienen bajo una carpeta
// (casi siempre "package/") que se saca. Solo se aceptan archivos y
// carpetas, y ninguna ruta puede salir de dir.
func extract(data []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		_, rel, ok := strings.Cut(name, "/")
		if !ok || rel == "" {
			continue // la carpeta de arriba misma
		}
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("ruta inválida en el tarball: %q", h.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, maxTarball))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			// Symlinks y demás: un paquete de JavaScript no los necesita.
		}
	}
}

// checkDeps avisa si algún paquete depende de otro que no está en el
// lockfile.
func checkDeps(pkgs []pkg, dir string) error {
	have := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		have[p.Name] = true
	}
	var missing []string
	for _, p := range pkgs {
		var pj struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p.Name), "package.json"))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &pj); err != nil {
			return fmt.Errorf("%s/package.json: %w", p.Name, err)
		}
		for dep, rng := range pj.Dependencies {
			if !have[dep] {
				missing = append(missing, fmt.Sprintf("%s (%s, lo pide %s)", dep, rng, p.Name))
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("faltan en el lockfile: %s", strings.Join(missing, "; "))
	}
	return nil
}

func getJSON(client *http.Client, url string, out any) error {
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 10<<20)).Decode(out)
}
