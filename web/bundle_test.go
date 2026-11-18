package web

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// El bundle commiteado tiene que salir de src/ tal como está: si alguien
// cambia src/ y se olvida de go generate, el binario llevaría el JS viejo.
// El test corre la misma línea de //go:generate de sveltebuild, con la
// salida en un directorio temporal, y compara archivo por archivo (los
// mismos nombres, que llevan el hash de los chunks, y el mismo contenido).
// El compilador de Svelte necesita Node: sin Node el test se saltea.
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
	if _, err := os.Stat("node_modules/svelte/.npmfetch"); err != nil {
		t.Skip("faltan los paquetes de npm (go generate ./web los baja)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("sin Node no se compila Svelte")
	}

	src, err := os.ReadFile("embed.go")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for l := range strings.Lines(string(src)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "//go:generate go run ../internal/sveltebuild "); ok {
			args = append([]string{"run", "../internal/sveltebuild"}, strings.Fields(rest)...)
		}
	}
	if args == nil {
		t.Fatal("no encontré la línea //go:generate de sveltebuild en embed.go")
	}
	var committed string
	out := filepath.Join(t.TempDir(), "app")
	for i, a := range args {
		if a == "-outdir" && i+1 < len(args) {
			committed, args[i+1] = args[i+1], out
		}
	}
	if committed == "" {
		t.Fatal("la línea de sveltebuild no tiene -outdir")
	}
	if b, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
	}

	names := func(dir string) []string {
		es, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range es {
			out = append(out, e.Name())
		}
		return out
	}
	built, have := names(out), names(committed)
	if !slices.Equal(built, have) {
		t.Fatalf("%s tiene %v y src/ da %v: correr go generate ./web", committed, have, built)
	}
	for _, n := range built {
		got, _ := os.ReadFile(filepath.Join(out, n))
		want, _ := os.ReadFile(filepath.Join(committed, n))
		if !bytes.Equal(got, want) {
			t.Errorf("%s/%s no está al día con src/: correr go generate ./web", committed, n)
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
