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
	// Por eso se leen acá también.
	entries, err := os.ReadDir("src")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := os.ReadFile(filepath.Join("src", e.Name())); err != nil {
			t.Fatal(err)
		}
	}

	src, err := os.ReadFile("embed.go")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for l := range strings.Lines(string(src)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "//go:generate go "); ok {
			args = strings.Fields(rest)
		}
	}
	if args == nil {
		t.Fatal("no encontré la línea //go:generate en embed.go")
	}

	var committed string
	out := filepath.Join(t.TempDir(), "out.js")
	for i, a := range args {
		if f, ok := strings.CutPrefix(a, "--outfile="); ok {
			committed, args[i] = f, "--outfile="+out
		}
	}
	cmd := exec.Command("go", append(args, "--log-level=warning")...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
	}

	got, _ := os.ReadFile(out)
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s no está al día con src/: correr go generate ./web", committed)
	}
}
