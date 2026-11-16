package web

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// El bundle commiteado tiene que salir de src/ tal como está: si alguien
// cambia src/ y se olvida de go generate, el binario llevaría el JS viejo.
// El test corre la misma línea de //go:generate, con la salida en un
// directorio temporal.
func TestBundleAlDia(t *testing.T) {
	if testing.Short() {
		t.Skip("compila el bundle")
	}
	// go test guarda en caché el resultado y solo lo invalida si cambian
	// los archivos que abrió este proceso: los que lee esbuild no cuentan.
	// Por eso se leen acá también, con npm.lock (que fija lo que hay en
	// node_modules).
	readSources(t)
	if _, err := os.ReadFile("npm.lock"); err != nil {
		t.Fatal(err)
	}
	// Los paquetes de npm no están en el repo: los baja go generate. Sin
	// ellos no se puede armar el bundle del editor.
	if _, err := os.Stat("node_modules/@codemirror/view/.npmfetch"); err != nil {
		t.Skip("faltan los paquetes de npm (go generate ./web los baja)")
	}

	src, err := os.ReadFile("embed.go")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for l := range strings.Lines(string(src)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "//go:generate go tool esbuild "); ok {
			args = append([]string{"tool", "esbuild"}, strings.Fields(rest)...)
		}
	}
	if args == nil {
		t.Fatal("no encontré la línea //go:generate de esbuild en embed.go")
	}

	var committed string
	out := t.TempDir()
	for i, a := range args {
		if f, ok := strings.CutPrefix(a, "--outdir="); ok {
			committed, args[i] = f, "--outdir="+out
		}
	}
	if committed == "" {
		t.Fatal("la línea de esbuild no tiene --outdir")
	}
	cmd := exec.Command("go", append(args, "--log-level=warning")...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
	}

	built, err := os.ReadDir(out)
	if err != nil || len(built) == 0 {
		t.Fatalf("esbuild no generó nada: %v", err)
	}
	for _, e := range built {
		got, _ := os.ReadFile(filepath.Join(out, e.Name()))
		want, err := os.ReadFile(filepath.Join(committed, e.Name()))
		if err != nil {
			t.Errorf("%s/%s no está commiteado: correr go generate ./web", committed, e.Name())
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s/%s no está al día con src/: correr go generate ./web", committed, e.Name())
		}
	}
}

// readSources lee todo src/ (con las subcarpetas): ver el comentario de
// TestBundleAlDia sobre la caché de go test.
func readSources(t *testing.T) {
	t.Helper()
	err := filepath.WalkDir("src", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		_, err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// La portada de Svelte, igual: el bundle commiteado tiene que salir de
// src/portada tal como está. Su compilador necesita Node; sin Node el test
// se saltea.
func TestPortadaAlDia(t *testing.T) {
	if testing.Short() {
		t.Skip("compila el bundle")
	}
	readSources(t)
	if _, err := os.ReadFile("npm.lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("node_modules/svelte/.npmfetch"); err != nil {
		t.Skip("faltan los paquetes de npm (go generate ./web los baja)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("sin Node no se compila Svelte")
	}
	out := t.TempDir()
	cmd := exec.Command("go", "run", "../internal/sveltebuild", "-outdir", out, "src/portada.js")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sveltebuild: %v\n%s", err, b)
	}
	got, _ := os.ReadFile(filepath.Join(out, "portada.js"))
	want, err := os.ReadFile("static/gen/portada.js")
	if err != nil {
		t.Fatal("static/gen/portada.js no está commiteado: correr go generate ./web")
	}
	if !bytes.Equal(got, want) {
		t.Error("static/gen/portada.js no está al día con src/portada: correr go generate ./web")
	}
}
