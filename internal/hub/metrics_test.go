package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func TestSystemValues(t *testing.T) {
	sys := agent.System{
		Metrics: &agent.HostMetrics{CPU: 12.5, MemUsed: 100, Load: [3]float64{1, 2, 3},
			Net:   []agent.NetRate{{Name: "eth0", Rx: 10, Tx: 20}},
			Disks: []agent.DiskRate{{Name: "sda", Read: 1, Write: 2}},
			FS:    []agent.FSUsage{{Mount: "/mnt/archivos", Used: 500, Total: 1000}},
			Temps: []agent.TempValue{{Chip: "coretemp", Label: "Core 0", Celsius: 40}}},
		Containers: []agent.ContainerStats{{Name: "web", CPU: 3, Mem: 7}},
		Guests:     []agent.Guest{{VMID: 102, Status: "running", CPU: 1, Mem: 9}, {VMID: 103, Status: "stopped"}},
	}
	v := systemValues("vm", sys)
	for k, want := range map[string]float64{
		"h/vm/cpu": 12.5, "h/vm/mem": 100, "h/vm/load1": 1, "h/vm/net/eth0/rx": 10, "h/vm/net/eth0/tx": 20,
		"h/vm/disk/sda/r": 1, "h/vm/disk/sda/w": 2, "h/vm/fs//mnt/archivos": 500, "h/vm/temp/coretemp/Core 0": 40,
		"c/vm/web/cpu": 3, "c/vm/web/mem": 7, "g/vm/102/cpu": 1, "g/vm/102/mem": 9,
	} {
		if got, ok := v[k]; !ok || got != want {
			t.Errorf("%s = %v (%v), quería %v", k, got, ok, want)
		}
	}
	if _, ok := v["h/vm/swap"]; ok {
		t.Error("sin swap no tiene que haber serie de swap")
	}
	if _, ok := v["g/vm/103/cpu"]; ok {
		t.Error("una LXC apagada no suma puntos")
	}
}

func TestAlignMetrics(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	pts := map[string][]store.MetricPoint{
		"a": {{At: t0, Avg: 1, Max: 2}, {At: t0.Add(time.Minute), Avg: 3, Max: 4}, {At: t0.Add(10 * time.Minute), Avg: 5, Max: 5}},
		"b": {{At: t0.Add(time.Minute), Avg: 7.12345, Max: 8}},
	}
	d := alignMetrics([]string{"a", "b", "c"}, pts, 60)
	wantT := []int64{t0.Unix(), t0.Unix() + 60, t0.Unix() + 120, t0.Unix() + 600}
	if len(d.T) != len(wantT) {
		t.Fatalf("T = %v, quería %v", d.T, wantT)
	}
	for i := range wantT {
		if d.T[i] != wantT[i] {
			t.Fatalf("T = %v, quería %v", d.T, wantT)
		}
	}
	val := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	a, b, c := d.Series[0], d.Series[1], d.Series[2]
	if val(a.Avg[0]) != 1.0 || val(a.Avg[1]) != 3.0 || a.Avg[2] != nil || val(a.Max[3]) != 5.0 {
		t.Errorf("a = %v", a)
	}
	if b.Avg[0] != nil || val(b.Avg[1]) != 7.123 {
		t.Errorf("b = %v", b)
	}
	if len(c.Avg) != 4 || c.Avg[0] != nil {
		t.Errorf("c = %v", c)
	}
}

func TestRecorderYRutaDeMetricas(t *testing.T) {
	snap := Snapshot{At: time.Now(), Hosts: []HostStatus{{Name: "vm", OK: true}}}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("una-contraseña-larga")
	if err := st.SetPassword(context.Background(), "admin", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	h, err := newHandler(fixedSnapshot(snap), nil, nil, &fakeMonitor{snap: snap}, st, auth.NewSessions(st, false, logger), testAPIToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rec := newRecorder(st, logger)
	now := time.Now().Truncate(time.Minute)
	sys := agent.System{At: now, Metrics: &agent.HostMetrics{CPU: 50}}
	rec.Record(Snapshot{Systems: map[string]agent.System{"vm": sys}})
	rec.Record(Snapshot{Systems: map[string]agent.System{"vm": sys}}) // la misma muestra: no se suma
	sys.At, sys.Metrics = now.Add(15*time.Second), &agent.HostMetrics{CPU: 30}
	rec.Record(Snapshot{Systems: map[string]agent.System{"vm": sys}})

	c := loggedIn(t, srv)
	res, err := c.Get(srv.URL + "/data/metrics?range=1h&key=h/vm/cpu")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var d metricsData
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", res.StatusCode, err)
	}
	if len(d.T) != 1 || *d.Series[0].Avg[0] != 40 || *d.Series[0].Max[0] != 50 {
		t.Fatalf("= %+v", d)
	}
	for _, bad := range []string{"/data/metrics?range=2h&key=h/vm/cpu", "/data/metrics?range=1h", "/data/metrics?key=x/y"} {
		res, _ := c.Get(srv.URL + bad)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, res.StatusCode)
		}
	}
}
