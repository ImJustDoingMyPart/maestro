// Command maestro es el panel de Maestro Monitor. Un solo binario con dos
// modos: "serve" (el hub web) y "agent" (uno por host, lee Docker y el sistema).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/collect"
	"github.com/ImJustDoingMyPart/maestro/internal/docker"
	"github.com/ImJustDoingMyPart/maestro/internal/httpserver"
	"github.com/ImJustDoingMyPart/maestro/internal/hub"
	"github.com/ImJustDoingMyPart/maestro/internal/journal"
	"github.com/ImJustDoingMyPart/maestro/internal/stacks"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
	"github.com/ImJustDoingMyPart/maestro/internal/systemd"
)

// version se inyecta al compilar:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/maestro
var version = ""

const usage = `uso: maestro <comando> [opciones]

comandos:
  serve          hub web (portada, login, checks, avisos)
  agent          agent de un host (Docker, /proc, systemd)
  passwd         crea el usuario del panel o le cambia la contraseña
  telegram-test  manda un aviso de prueba a los chats de telegram: en maestro.yaml
  version        imprime la versión

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
	case "passwd":
		return runPasswd(ctx, rest, os.Stdin, stderr)
	case "telegram-test":
		fs := flag.NewFlagSet("telegram-test", flag.ContinueOnError)
		fs.SetOutput(stderr)
		cfgPath := fs.String("config", "maestro.yaml", "archivo de configuración")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return hub.TelegramTest(ctx, *cfgPath, stdout)
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
	cfgPath := fs.String("config", "maestro.yaml", "archivo de configuración")
	dbPath := fs.String("db", "maestro.db", "base SQLite")
	dev := fs.Bool("dev", false, "cookie sin Secure, para probar por HTTP en local")
	demo := fs.Bool("demo", false, "portada con servicios falsos, sin login ni agents")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	if *demo {
		return hub.ServeDemo(ctx, *listen, logger)
	}
	return hub.Serve(ctx, hub.Options{Listen: *listen, ConfigPath: *cfgPath, DBPath: *dbPath, Dev: *dev}, logger)
}

// runPasswd crea el usuario o le cambia la contraseña. La contraseña se pide
// por la terminal, sin eco; si stdin no es una terminal (un script, un
// docker exec sin -t) se lee la primera línea.
func runPasswd(ctx context.Context, args []string, stdin *os.File, stderr io.Writer) error {
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "maestro.db", "base SQLite")
	user := fs.String("user", "admin", "usuario")
	if err := fs.Parse(args); err != nil {
		return err
	}

	password, err := readPassword(stdin, stderr)
	if err != nil {
		return err
	}
	if len([]rune(password)) < auth.MinPasswordLen {
		return fmt.Errorf("passwd: %w", auth.ErrWeakPassword)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetPassword(ctx, *user, hash, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "contraseña de %q guardada en %s; sus sesiones abiertas se cerraron\n", *user, *dbPath)
	return nil
}

func readPassword(stdin *os.File, stderr io.Writer) (string, error) {
	fd := int(stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("passwd: leer la contraseña de stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(stderr, "Contraseña: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(stderr, "Repetila: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("passwd: las contraseñas no coinciden")
	}
	return string(first), nil
}

func runAgent(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:9101", "dirección donde escucha el agent")
	stacksDir := fs.String("stacks-dir", "", "carpeta de los stacks de compose; sin ella, el agent no edita ni levanta stacks")
	gitAuthor := fs.String("git-author", stacks.DefaultAuthor, "autor de los commits al guardar un archivo de un stack")
	useDocker := fs.Bool("docker", true, "lee Docker (contenedores, logs, stacks); false en un host sin Docker")
	useSystemd := fs.Bool("systemd", true, "lee las unidades y timers de systemd por el bus del sistema")
	proxmox := fs.Bool("proxmox", false, "lee las LXC y VMs con pvesh (en el host de Proxmox, como root)")
	smart := fs.Bool("smart", false, "lee el SMART de los discos con smartctl (como root)")
	procDir := fs.String("proc", "/proc", "carpeta de /proc")
	sysDir := fs.String("sys", "/sys", "carpeta de /sys")
	hostFS := fs.String("hostfs", "", "dónde está montada la raíz del host si el agent corre en un contenedor (p. ej. /hostfs)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// El token va por entorno y no por flag: los flags se ven en ps.
	token := os.Getenv("MAESTRO_AGENT_TOKEN")
	if token == "" {
		return errors.New("agent: falta MAESTRO_AGENT_TOKEN (generalo con: openssl rand -hex 32)")
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	opts := collect.Options{Proc: *procDir, Sys: *sysDir, HostFS: *hostFS, Proxmox: *proxmox, SMART: *smart, Logger: logger}

	// containers y stacksSrc son interfaces: quedan nil sin Docker o sin
	// --stacks-dir. Ojo con asignarles un puntero nil: una interfaz que
	// guarda un puntero nil no es nil, y el handler creería que hay.
	var (
		containers agent.ContainerSource
		stacksSrc  agent.StackSource
	)
	if *useDocker {
		dc, err := docker.New()
		if err != nil {
			return fmt.Errorf("agent: cliente de Docker: %w", err)
		}
		defer dc.Close()
		// Los eventos "die" dan el exit code de un reinicio, que el
		// inspect ya no tiene cuando el contenedor volvió a arrancar.
		go dc.WatchExits(ctx, logger)
		containers, opts.Docker = dc, dc
		if *stacksDir != "" {
			m, err := stacks.New(stacks.Options{Root: *stacksDir, Author: *gitAuthor}, dc, logger)
			if err != nil {
				return fmt.Errorf("agent: %w", err)
			}
			stacksSrc = m
		}
	} else if *stacksDir != "" {
		return errors.New("agent: --stacks-dir necesita Docker (sacá --docker=false)")
	}
	if *useSystemd {
		r := systemd.New()
		defer r.Close()
		opts.Units = r
	}
	col := collect.New(opts)
	go col.Run(ctx)

	var hopts []agent.HandlerOption
	if *useSystemd {
		// El journal de las unidades: journalctl del PATH o, en un
		// contenedor, el del host en --hostfs.
		j := journal.New(*hostFS)
		if !j.Available() {
			logger.Warn("sin journalctl: el journal de las unidades no se va a ver")
		}
		hopts = append(hopts, agent.WithJournal(j))
	}
	h, err := agent.NewHandler(containers, stacksSrc, col, token, logger, hopts...)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return httpserver.Run(ctx, *listen, h, logger)
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
