package hub

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/journal"
)

// fakeJournal es un logOpener que también lee el journal: tiene una lista
// de entradas y los cursores son su posición.
type fakeJournal struct {
	fakeLogs
	jmu     sync.Mutex
	entries []journal.Entry
	units   [][]string
	fail    bool
}

func (f *fakeJournal) Journal(_ context.Context, host string, units []string, n int, after string) (journal.Page, error) {
	f.jmu.Lock()
	defer f.jmu.Unlock()
	f.units = append(f.units, units)
	if f.fail {
		return journal.Page{}, errors.New("agent caído")
	}
	from := max(len(f.entries)-n, 0)
	if after != "" {
		from = int(after[0] - '0')
	}
	p := journal.Page{Entries: slices.Clone(f.entries[from:])}
	if len(f.entries) > 0 {
		p.Cursor = string(rune('0' + len(f.entries)))
	}
	return p, nil
}

func (f *fakeJournal) add(e journal.Entry) {
	f.jmu.Lock()
	f.entries = append(f.entries, e)
	f.jmu.Unlock()
}

func TestStreamJournal(t *testing.T) {
	old := journalPoll
	journalPoll = 20 * time.Millisecond
	t.Cleanup(func() { journalPoll = old })

	snap := Snapshot{Services: []catalog.Service{{
		ID: "found/vm/unit/restic-backup.timer", Source: catalog.SourceSystemd, Name: "Restic", Host: "vm", Unit: "restic-backup.timer",
	}}}
	j := &fakeJournal{entries: []journal.Entry{
		{TS: t0, Priority: 6, Ident: "systemd", Message: "Starting restic-backup.service"},
		{TS: t0.Add(time.Second), Priority: 3, Ident: "backup.sh", Message: "Fatal: unable to open repository"},
	}}
	srv := testServerLogs(t, snap, j)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/found/vm/unit/restic-backup.timer", "")

	readJ := func() logEvent {
		t.Helper()
		ev := r.next(t)
		var l logEvent
		if ev.Event != "" || json.Unmarshal([]byte(ev.Data), &l) != nil {
			t.Fatalf("quería una línea, llegó %+v", ev)
		}
		return l
	}
	if l := readJ(); l.Line != "Starting restic-backup.service" || l.Container != "systemd" || l.Level != "" {
		t.Errorf("primera = %+v", l)
	}
	if l := readJ(); l.Level != "error" || l.Stream != "stderr" || l.Container != "backup.sh" {
		t.Errorf("prioridad 3 = %+v", l)
	}
	j.jmu.Lock()
	if u := j.units[0]; len(u) != 2 || u[1] != "restic-backup.service" {
		t.Errorf("un timer tiene que pedir también su servicio: %v", u)
	}
	j.jmu.Unlock()

	// Una línea nueva llega en la próxima vuelta, desde el cursor.
	j.add(journal.Entry{TS: t0.Add(time.Minute), Priority: 4, Ident: "backup.sh", Message: "1 archivo no se pudo leer"})
	if l := readJ(); l.Line != "1 archivo no se pudo leer" || l.Level != "warn" {
		t.Errorf("nueva = %+v", l)
	}

	// Si el agent deja de dar el journal se avisa una vez, y al volver
	// también.
	j.jmu.Lock()
	j.fail = true
	j.jmu.Unlock()
	if ev := r.next(t); ev.Event != "notice" {
		t.Fatalf("quería un aviso, llegó %+v", ev)
	}
	j.jmu.Lock()
	j.fail = false
	j.jmu.Unlock()
	j.add(journal.Entry{TS: t0.Add(2 * time.Minute), Message: "volvió"})
	if ev := r.next(t); ev.Event != "notice" {
		t.Fatalf("quería el aviso de que volvió, llegó %+v", ev)
	}
	if l := readJ(); l.Line != "volvió" {
		t.Errorf("después del corte = %+v", l)
	}
}
