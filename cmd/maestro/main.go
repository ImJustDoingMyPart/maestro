// Command maestro es el panel de Maestro Monitor. Un solo binario con dos
// modos: "serve" (el hub web) y "agent" (uno por host, lee Docker y el sistema).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/ImJustDoingMyPart/maestro/internal/hub"
)

// version se inyecta al compilar:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/maestro
var version = ""

const usage = `uso: maestro <comando> [opciones]

comandos:
  serve     hub web (portada, login, checks)
  agent     agent de un host (Docker, /proc, systemd)
  version   imprime la versión

"maestro <comando> -h" muestra las opciones de cada comando.
`

func main() {
	// El contexto se cancela con Ctrl+C o con SIGTERM (docker stop), y cada
	// servidor lo usa para apagarse ordenado.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "maestro:", err)
		}
		os.Exit(2)
	}
}

// run despacha el subcomando. Recibe argumentos y salidas en vez de leer los
// globales de os para poder probarlo.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("falta el comando")
	}

	switch cmd, rest := args[0], args[1:]; cmd {
	case "serve":
		return runServe(ctx, rest, stderr)
	case "agent":
		return runAgent(ctx, rest, stderr)
	case "version":
		fmt.Fprintln(stdout, currentVersion())
		return nil
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("comando desconocido: %q", cmd)
	}
}

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8090", "dirección donde escucha la web")
	demo := fs.Bool("demo", false, "portada con servicios falsos, sin login ni agents")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*demo {
		return errors.New("serve: por ahora solo existe el modo --demo")
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	return hub.ServeDemo(ctx, *listen, logger)
}

func runAgent(_ context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_ = fs.String("listen", "127.0.0.1:9100", "dirección donde escucha el agent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return errors.New("agent: todavía no implementado (etapa 1, paso 5)")
}

// currentVersion prefiere la versión de -ldflags; si no la hay (go run, go
// build sin flags), usa la revisión de git que Go guarda en el binario.
func currentVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "desconocida"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return "dev-" + rev + dirty
}
