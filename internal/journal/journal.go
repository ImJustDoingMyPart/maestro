// Package journal lee el journal de systemd corriendo journalctl.
//
// No hay un lector del formato del journal en Go puro que use sus índices
// (buscar por unidad sin recorrer todo el archivo), así que el agent llama
// a journalctl -o json. En un host nativo está en el PATH. En un agent
// dentro de un contenedor (Alpine, sin systemd) se usa el del host, montado
// en --hostfs: se lo corre con el cargador dinámico del propio host y sus
// bibliotecas, apuntando a sus archivos del journal. Para leerlos, el
// proceso tiene que estar en el grupo systemd-journal del host (group_add
// en el compose).
package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Entry es una línea del journal.
type Entry struct {
	TS       time.Time `json:"ts"`
	Priority int       `json:"prio"` // 0 emerg … 7 debug, como syslog
	Ident    string    `json:"ident,omitempty"`
	Unit     string    `json:"unit,omitempty"`
	Message  string    `json:"msg"`
}

// Page es lo que devuelve una lectura: las líneas, de la más vieja a la
// más nueva, y el cursor de la última, para pedir lo que siga.
type Page struct {
	Entries []Entry `json:"entries"`
	Cursor  string  `json:"cursor,omitempty"`
}

// MaxLines acota una lectura: con cursor, si se juntó mucho desde la
// anterior, se devuelven las primeras MaxLines y el cursor sigue desde ahí.
const MaxLines = 2000

// ErrUnavailable sale si no hay journalctl con qué leer.
var ErrUnavailable = errors.New("no hay journalctl en este host")

// Reader corre journalctl.
type Reader struct {
	cmd []string // el ejecutable y los argumentos fijos
}

// New busca con qué leer el journal: journalctl en el PATH, o el del host
// montado en hostfs. Si no hay ninguno, el Reader devuelve ErrUnavailable.
func New(hostfs string) *Reader {
	if p, err := exec.LookPath("journalctl"); err == nil {
		return &Reader{cmd: []string{p}}
	}
	if hostfs == "" {
		return &Reader{}
	}
	return &Reader{cmd: hostCommand(hostfs)}
}

// hostCommand arma la llamada al journalctl del host a través de su
// cargador dinámico. Devuelve nil si en hostfs no está todo.
func hostCommand(hostfs string) []string {
	bin := filepath.Join(hostfs, "usr/bin/journalctl")
	if _, err := os.Stat(bin); err != nil {
		return nil
	}
	var loader string
	for _, l := range []string{"lib64/ld-linux-x86-64.so.2", "lib/ld-linux-aarch64.so.1", "lib/ld-linux-armhf.so.3"} {
		if _, err := os.Stat(filepath.Join(hostfs, l)); err == nil {
			loader = filepath.Join(hostfs, l)
			break
		}
	}
	if loader == "" {
		return nil
	}
	var libs []string
	for _, d := range []string{
		"usr/lib/x86_64-linux-gnu/systemd", "usr/lib/x86_64-linux-gnu", "lib/x86_64-linux-gnu",
		"usr/lib/aarch64-linux-gnu/systemd", "usr/lib/aarch64-linux-gnu",
		"usr/lib64/systemd", "usr/lib64", "usr/lib/systemd", "usr/lib",
	} {
		if st, err := os.Stat(filepath.Join(hostfs, d)); err == nil && st.IsDir() {
			libs = append(libs, filepath.Join(hostfs, d))
		}
	}
	return []string{loader, "--library-path", strings.Join(libs, ":"), bin,
		"-D", filepath.Join(hostfs, "var/log/journal")}
}

// Available dice si hay con qué leer.
func (r *Reader) Available() bool { return len(r.cmd) > 0 }

// validUnit es un nombre de unidad de systemd. Que no pueda empezar con "-"
// importa: va como argumento de journalctl.
var validUnit = regexp.MustCompile(`^[A-Za-z0-9:_.@\\][A-Za-z0-9:_.@\\-]*$`)

// ValidUnit dice si name se puede pasar como unidad.
func ValidUnit(name string) bool { return len(name) <= 256 && validUnit.MatchString(name) }

// Read lee las líneas de las unidades (cualquiera de ellas). Sin after,
// las últimas n; con after (un cursor de una lectura anterior), las que
// siguen, hasta MaxLines.
func (r *Reader) Read(ctx context.Context, units []string, n int, after string) (Page, error) {
	if !r.Available() {
		return Page{}, ErrUnavailable
	}
	if len(units) == 0 {
		return Page{}, errors.New("falta la unidad")
	}
	args := append([]string(nil), r.cmd[1:]...)
	args = append(args, "-o", "json", "--no-pager", "--output-fields=MESSAGE,PRIORITY,SYSLOG_IDENTIFIER,_SYSTEMD_UNIT,UNIT")
	for _, u := range units {
		if !ValidUnit(u) {
			return Page{}, fmt.Errorf("unidad inválida: %q", u)
		}
		args = append(args, "-u", u)
	}
	if after != "" {
		if strings.ContainsAny(after, "\n\x00") || len(after) > 512 {
			return Page{}, errors.New("cursor inválido")
		}
		args = append(args, "--after-cursor="+after)
	} else {
		n = min(max(n, 1), MaxLines)
		args = append(args, "-n", strconv.Itoa(n))
	}

	cmd := exec.CommandContext(ctx, r.cmd[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Page{}, err
	}
	if err := cmd.Start(); err != nil {
		return Page{}, fmt.Errorf("journalctl: %w", err)
	}
	page, perr := parse(out)
	if perr != nil {
		_ = cmd.Process.Kill() // MaxLines: no hace falta el resto
	}
	werr := cmd.Wait()
	if errors.Is(perr, errFull) {
		return page, nil
	}
	if perr != nil {
		return Page{}, perr
	}
	if werr != nil {
		msg := strings.TrimSpace(stderr.String())
		return Page{}, fmt.Errorf("journalctl: %v: %s", werr, lastLine(msg))
	}
	return page, nil
}

var errFull = errors.New("tope de líneas")

// parse lee la salida de journalctl -o json: un objeto por línea.
func parse(r interface{ Read([]byte) (int, error) }) (Page, error) {
	var p Page
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			continue // journalctl no escribe otra cosa, pero por las dudas
		}
		e := Entry{
			Message: field(raw["MESSAGE"]),
			Ident:   field(raw["SYSLOG_IDENTIFIER"]),
			Unit:    field(raw["_SYSTEMD_UNIT"]),
		}
		if e.Unit == "" {
			e.Unit = field(raw["UNIT"]) // los mensajes de systemd sobre la unidad
		}
		e.Priority = 6
		if v, err := strconv.Atoi(field(raw["PRIORITY"])); err == nil {
			e.Priority = v
		}
		if us, err := strconv.ParseInt(field(raw["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
			e.TS = time.UnixMicro(us)
		}
		p.Entries = append(p.Entries, e)
		p.Cursor = field(raw["__CURSOR"])
		if len(p.Entries) >= MaxLines {
			return p, errFull
		}
	}
	return p, sc.Err()
}

// field pasa un campo del JSON de journalctl a texto. Es un string, o un
// arreglo de bytes si el mensaje no era UTF-8 válido o tenía caracteres de
// control, o null si era demasiado largo.
func field(m json.RawMessage) string {
	if len(m) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		return s
	}
	var b []byte
	var nums []int
	if json.Unmarshal(m, &nums) == nil {
		for _, n := range nums {
			b = append(b, byte(n))
		}
		return strings.ToValidUTF8(string(b), "�")
	}
	return ""
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// UnitsFor son las unidades cuyo journal se muestra para name: un timer no
// escribe casi nada, lo que importa es lo que escribe el servicio que
// dispara (el del mismo nombre).
func UnitsFor(name string) []string {
	if base, ok := strings.CutSuffix(name, ".timer"); ok {
		return []string{name, base + ".service"}
	}
	return []string{name}
}
