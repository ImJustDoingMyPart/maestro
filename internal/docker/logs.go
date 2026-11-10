package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"
	"time"
	"unicode/utf8"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// maxLineLen es el largo máximo de una línea: lo que pasa de ahí sale como
// otra línea. Además, lo que se junta sin salto se manda al llegar a este
// largo, así un contenedor que escribe binario no llena la memoria del agent.
const maxLineLen = 64 << 10

// errStop avisa, desde un Write, que quien recorre el iterador cortó.
var errStop = errors.New("corte del consumidor")

// Logs abre los logs de un contenedor con las marcas de tiempo de Docker.
func (c *Client) Logs(ctx context.Context, id string, opts agent.LogOptions) (iter.Seq2[agent.LogLine, error], error) {
	// El inspect dice si el contenedor tiene TTY, que cambia el formato del
	// stream, y de paso distingue "no existe" de otros errores.
	ins, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil, agent.ErrContainerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("inspeccionar %s: %w", id, err)
	}
	tty := ins.Container.Config != nil && ins.Container.Config.Tty

	o := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     opts.Follow,
		Tail:       strconv.Itoa(opts.Tail),
	}
	if !opts.Since.IsZero() {
		o.Since = opts.Since.UTC().Format(time.RFC3339Nano)
	}
	rc, err := c.api.ContainerLogs(ctx, id, o)
	if cerrdefs.IsNotFound(err) {
		return nil, agent.ErrContainerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("logs de %s: %w", id, err)
	}

	return func(yield func(agent.LogLine, error) bool) {
		defer rc.Close()
		err := readLogs(rc, tty, func(l agent.LogLine) bool { return yield(l, nil) })
		if err != nil && !errors.Is(err, errStop) {
			yield(agent.LogLine{}, err)
		}
	}, nil
}

// readLogs parte el stream de Docker en líneas y llama a emit con cada una
// hasta el final del stream, un error de lectura o que emit devuelva false
// (entonces sale con errStop).
//
// Sin TTY, Docker multiplexa stdout y stderr en tramas con un encabezado de
// 8 bytes, y stdcopy.StdCopy las separa en dos io.Writer. Con TTY no hay
// tramas y todo es stdout.
func readLogs(r io.Reader, tty bool, emit func(agent.LogLine) bool) error {
	out := &lineWriter{stream: "stdout", emit: emit}
	if tty {
		if _, err := io.Copy(out, r); err != nil {
			return err
		}
		return out.flush()
	}

	errw := &lineWriter{stream: "stderr", emit: emit}
	if _, err := stdcopy.StdCopy(out, errw, r); err != nil {
		return err
	}
	if err := out.flush(); err != nil {
		return err
	}
	return errw.flush()
}

// lineWriter es un io.Writer que junta bytes hasta tener líneas completas.
// Un Write puede traer media línea o varias, así que lo que queda sin salto
// espera al próximo.
type lineWriter struct {
	stream string
	buf    []byte
	emit   func(agent.LogLine) bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	start := 0
	for {
		i := bytes.IndexByte(w.buf[start:], '\n')
		if i < 0 {
			break
		}
		if !w.send(w.buf[start : start+i]) {
			return len(p), errStop
		}
		start += i + 1
	}
	// Lo pendiente se corre al principio para reusar la memoria del buffer.
	w.buf = w.buf[:copy(w.buf, w.buf[start:])]

	if len(w.buf) >= maxLineLen {
		cut := runeCut(w.buf, maxLineLen)
		if !w.send(w.buf[:cut]) {
			return len(p), errStop
		}
		w.buf = w.buf[:copy(w.buf, w.buf[cut:])]
	}
	return len(p), nil
}

// flush manda lo que quedó sin salto al final del stream.
func (w *lineWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	ok := w.send(w.buf)
	w.buf = w.buf[:0]
	if !ok {
		return errStop
	}
	return nil
}

// send separa la marca de tiempo que antepone Docker ("2026-11-10T14:03:07.123456789Z ")
// y saca el \r que deja una TTY. Si la marca no parsea, la línea va entera
// y sin hora. Una línea de más de maxLineLen sale en pedazos con la misma
// hora.
func (w *lineWriter) send(b []byte) bool {
	b = bytes.TrimSuffix(b, []byte("\r"))
	var ts time.Time
	head, rest, _ := bytes.Cut(b, []byte(" ")) // una línea vacía puede venir sin espacio
	if t, err := time.Parse(time.RFC3339Nano, string(head)); err == nil {
		ts, b = t, rest
	}
	for len(b) > maxLineLen {
		cut := runeCut(b, maxLineLen)
		if !w.emit(agent.LogLine{TS: ts, Stream: w.stream, Line: string(b[:cut])}) {
			return false
		}
		b = b[cut:]
	}
	return w.emit(agent.LogLine{TS: ts, Stream: w.stream, Line: string(b)})
}

// runeCut devuelve n, o un poco menos para no partir un carácter de UTF-8
// al medio. Pide len(b) >= n; si b no es UTF-8, corta en n.
func runeCut(b []byte, n int) int {
	if n == len(b) {
		return n
	}
	for cut := n; cut > n-utf8.UTFMax; cut-- {
		if utf8.RuneStart(b[cut]) {
			return cut
		}
	}
	return n
}
