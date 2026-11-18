package journal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	in := strings.Join([]string{
		`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1800000000000000","PRIORITY":"6","SYSLOG_IDENTIFIER":"restic","_SYSTEMD_UNIT":"restic-backup.service","MESSAGE":"snapshot 1a2b guardado"}`,
		`{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1800000001000000","PRIORITY":"3","SYSLOG_IDENTIFIER":"systemd","UNIT":"restic-backup.service","MESSAGE":[104,111,108,97,7]}`,
		`no es json`,
		`{"__CURSOR":"c3","__REALTIME_TIMESTAMP":"1800000002000000","MESSAGE":null}`,
	}, "\n")
	p, err := parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 3 || p.Cursor != "c3" {
		t.Fatalf("parse = %+v", p)
	}
	e := p.Entries[0]
	if e.Message != "snapshot 1a2b guardado" || e.Priority != 6 || e.Ident != "restic" || e.Unit != "restic-backup.service" || !e.TS.Equal(time.Unix(1_800_000_000, 0)) {
		t.Errorf("primera = %+v", e)
	}
	if e := p.Entries[1]; e.Message != "hola\a" || e.Priority != 3 || e.Unit != "restic-backup.service" {
		t.Errorf("segunda (bytes, UNIT) = %+v", e)
	}
	if e := p.Entries[2]; e.Message != "" || e.Priority != 6 {
		t.Errorf("tercera (sin prioridad, mensaje null) = %+v", e)
	}
}

func TestValidUnit(t *testing.T) {
	for _, u := range []string{"restic-backup.timer", "getty@tty1.service", `dev-disk-by\x2duuid.swap`, "AdGuardHome.service"} {
		if !ValidUnit(u) {
			t.Errorf("%q tendría que valer", u)
		}
	}
	for _, u := range []string{"", "-f", "--root=/", "a b", "a\nb"} {
		if ValidUnit(u) {
			t.Errorf("%q no tendría que valer", u)
		}
	}
}

func TestUnitsFor(t *testing.T) {
	if got := UnitsFor("restic-backup.timer"); len(got) != 2 || got[1] != "restic-backup.service" {
		t.Errorf("timer = %v", got)
	}
	if got := UnitsFor("AdGuardHome.service"); len(got) != 1 {
		t.Errorf("service = %v", got)
	}
}

// TestReadConJournalctlFalso corre Read contra un journalctl de mentira
// (un script) para ver los argumentos y la lectura entera.
func TestReadConJournalctlFalso(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "journalctl")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+args+"\necho '{\"__CURSOR\":\"z\",\"__REALTIME_TIMESTAMP\":\"1\",\"MESSAGE\":\"hola\"}'\n"), 0o755)
	r := &Reader{cmd: []string{script}}
	p, err := r.Read(context.Background(), []string{"a.timer", "a.service"}, 50, "")
	if err != nil || len(p.Entries) != 1 || p.Cursor != "z" {
		t.Fatalf("Read = %+v, %v", p, err)
	}
	b, _ := os.ReadFile(args)
	if got := string(b); !strings.Contains(got, "-u a.timer -u a.service -n 50") || !strings.Contains(got, "-o json") {
		t.Errorf("argumentos = %q", got)
	}
	if _, err := r.Read(context.Background(), []string{"a.service"}, 0, "z"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); !strings.Contains(string(b), "--after-cursor=z") || strings.Contains(string(b), " -n ") {
		t.Errorf("con cursor = %q", b)
	}
	if _, err := r.Read(context.Background(), []string{"-f"}, 1, ""); err == nil {
		t.Error("una unidad que empieza con - no tiene que llegar a journalctl")
	}
	if _, err := (&Reader{}).Read(context.Background(), []string{"a.service"}, 1, ""); err != ErrUnavailable {
		t.Errorf("sin journalctl = %v", err)
	}
}
