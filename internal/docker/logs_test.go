package docker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// frame arma una trama del stream multiplexado de Docker: 1 byte de stream
// (1 stdout, 2 stderr), 3 en cero y el largo en 4 bytes big endian.
func frame(stream byte, payload string) []byte {
	h := make([]byte, 8, 8+len(payload))
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func collect(t *testing.T, r io.Reader, tty bool) []agent.LogLine {
	t.Helper()
	var got []agent.LogLine
	if err := readLogs(r, tty, func(l agent.LogLine) bool { got = append(got, l); return true }); err != nil {
		t.Fatal(err)
	}
	return got
}

var (
	ts1 = time.Date(2026, 11, 10, 14, 3, 7, 123456789, time.UTC)
	ts2 = time.Date(2026, 11, 10, 14, 3, 8, 0, time.UTC)
)

const (
	s1 = "2026-11-10T14:03:07.123456789Z"
	s2 = "2026-11-10T14:03:08Z"
)

func TestReadLogs(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		tty  bool
		want []agent.LogLine
	}{
		{
			name: "multiplexado",
			in: bytes.Join([][]byte{
				frame(1, s1+" arrancando\n"),
				frame(2, s2+" level=error msg=boom\n"),
			}, nil),
			want: []agent.LogLine{
				{TS: ts1, Stream: "stdout", Line: "arrancando"},
				{TS: ts2, Stream: "stderr", Line: "level=error msg=boom"},
			},
		},
		{
			name: "una línea partida en dos tramas",
			in:   append(frame(1, s1+" mit"), frame(1, "ad\n")...),
			want: []agent.LogLine{{TS: ts1, Stream: "stdout", Line: "mitad"}},
		},
		{
			name: "varias líneas en una trama",
			in:   frame(1, s1+" a\n"+s2+" b\n"),
			want: []agent.LogLine{
				{TS: ts1, Stream: "stdout", Line: "a"},
				{TS: ts2, Stream: "stdout", Line: "b"},
			},
		},
		{
			name: "último sin salto",
			in:   frame(2, s1+" sin final"),
			want: []agent.LogLine{{TS: ts1, Stream: "stderr", Line: "sin final"}},
		},
		{
			name: "línea vacía, con y sin espacio",
			in:   frame(1, s1+" \n"+s2+"\n"),
			want: []agent.LogLine{
				{TS: ts1, Stream: "stdout", Line: ""},
				{TS: ts2, Stream: "stdout", Line: ""},
			},
		},
		{
			name: "sin marca de tiempo",
			in:   frame(1, "hola mundo\n"),
			want: []agent.LogLine{{Stream: "stdout", Line: "hola mundo"}},
		},
		{
			name: "tty: sin tramas y con \\r\\n",
			in:   []byte(s1 + " uno\r\n" + s2 + " dos\r\n"),
			tty:  true,
			want: []agent.LogLine{
				{TS: ts1, Stream: "stdout", Line: "uno"},
				{TS: ts2, Stream: "stdout", Line: "dos"},
			},
		},
		{
			name: "vacío",
			in:   nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// OneByteReader: el resultado no puede depender de cómo vienen
			// cortadas las lecturas.
			for _, r := range []io.Reader{bytes.NewReader(tt.in), iotest.OneByteReader(bytes.NewReader(tt.in))} {
				got := collect(t, r, tt.tty)
				if !equal(got, tt.want) {
					t.Errorf("readLogs:\n got %+v\nwant %+v", got, tt.want)
				}
			}
		})
	}
}

func TestReadLogsLineaGigante(t *testing.T) {
	long := strings.Repeat("x", maxLineLen+10)
	for _, r := range []io.Reader{
		bytes.NewReader(frame(1, s1+" "+long+"\n")),                       // entera en un Write
		iotest.OneByteReader(bytes.NewReader(frame(1, s1+" "+long+"\n"))), // de a un byte
	} {
		got := collect(t, r, false)
		if len(got) != 2 || len(got[0].Line)+len(got[1].Line) != len(long) || len(got[0].Line) > maxLineLen {
			t.Errorf("quería la línea cortada en dos de hasta %d; got %d líneas", maxLineLen, len(got))
		}
	}
}

// El corte no parte un carácter de varios bytes.
func TestReadLogsCorteUTF8(t *testing.T) {
	long := strings.Repeat("x", maxLineLen-1) + "ñ" + "fin" // la ñ cruza el límite
	got := collect(t, bytes.NewReader(frame(1, s1+" "+long+"\n")), false)
	if len(got) != 2 || got[0].Line+got[1].Line != long || !got[1].TS.Equal(ts1) {
		t.Errorf("got %d pedazos; quería 2 que juntos den la línea, con la misma hora", len(got))
	}
	checkUTF8(t, got)

	// Sin salto y de a un byte: corta el buffer, no send.
	long = strings.Repeat("x", maxLineLen-1) + "ñ" + "fin"
	got = collect(t, iotest.OneByteReader(bytes.NewReader(frame(1, long))), false)
	if len(got) != 2 || got[0].Line+got[1].Line != long {
		t.Errorf("sin salto: got %d pedazos; quería 2 que juntos den la línea", len(got))
	}
	checkUTF8(t, got)
}

func checkUTF8(t *testing.T, ls []agent.LogLine) {
	t.Helper()
	for _, l := range ls {
		if !utf8.ValidString(l.Line) {
			t.Errorf("pedazo con UTF-8 inválido: …%q", l.Line[max(0, len(l.Line)-4):])
		}
	}
}

// Si quien consume corta, readLogs deja de leer y avisa con errStop.
func TestReadLogsCorte(t *testing.T) {
	in := frame(1, s1+" a\n"+s1+" b\n"+s1+" c\n")
	var n int
	err := readLogs(bytes.NewReader(in), false, func(agent.LogLine) bool { n++; return n < 2 })
	if !errors.Is(err, errStop) || n != 2 {
		t.Errorf("err = %v tras %d líneas; quería errStop tras 2", err, n)
	}
}

func TestReadLogsErrorDeLectura(t *testing.T) {
	boom := errors.New("conexión cortada")
	r := io.MultiReader(bytes.NewReader(frame(1, s1+" a\n")), iotest.ErrReader(boom))
	var got []agent.LogLine
	err := readLogs(r, false, func(l agent.LogLine) bool { got = append(got, l); return true })
	if !errors.Is(err, boom) || len(got) != 1 {
		t.Errorf("err = %v con %d líneas; quería el error de lectura tras 1", err, len(got))
	}
}

func equal(a, b []agent.LogLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].TS.Equal(b[i].TS) || a[i].Stream != b[i].Stream || a[i].Line != b[i].Line {
			return false
		}
	}
	return true
}
