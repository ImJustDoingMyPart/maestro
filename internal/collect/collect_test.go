package collect

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestParseGuests(t *testing.T) {
	in := `[{"cpu":0.25,"id":"qemu/100","maxcpu":8,"maxmem":12884901888,"mem":8082894848,"name":"Debian-Server","status":"running","template":0,"type":"qemu","uptime":224096,"vmid":100},
	{"cpu":0,"id":"lxc/103","maxcpu":2,"maxmem":536870912,"mem":0,"name":"vpn","status":"stopped","template":0,"type":"lxc","uptime":0,"vmid":103},
	{"cpu":0,"id":"lxc/102","maxcpu":1,"maxmem":1073741824,"mem":126525440,"name":"dns","status":"running","type":"lxc","uptime":10,"vmid":102},
	{"id":"qemu/900","name":"plantilla","status":"stopped","template":1,"type":"qemu","vmid":900}]`
	got, err := ParseGuests([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.Guest{
		{VMID: 100, Kind: "vm", Name: "Debian-Server", Status: "running", CPU: 25, CPUs: 8, Mem: 8082894848, MaxMem: 12884901888, UptimeS: 224096},
		{VMID: 102, Kind: "lxc", Name: "dns", Status: "running", CPUs: 1, Mem: 126525440, MaxMem: 1073741824, UptimeS: 10},
		{VMID: 103, Kind: "lxc", Name: "vpn", Status: "stopped", CPUs: 2, MaxMem: 536870912},
	}
	if len(got) != len(want) {
		t.Fatalf("= %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v\nquería %+v", i, got[i], want[i])
		}
	}
	if _, err := ParseGuests([]byte("no es json")); err == nil {
		t.Fatal("quería error")
	}
}

func TestParseSMART(t *testing.T) {
	ata := `{"smartctl":{"exit_status":0},"power_mode":{"name":"ACTIVE or IDLE"},"model_name":"ADATA SU650NS38","serial_number":"X1",
	"smart_status":{"passed":true},"temperature":{"current":33},"power_on_time":{"hours":12494},
	"ata_smart_attributes":{"table":[{"id":5,"raw":{"value":0}},{"id":9,"raw":{"value":12494}},{"id":197,"raw":{"value":2}},{"id":198,"raw":{"value":1}}]}}`
	d, err := ParseSMART([]byte(ata))
	if err != nil {
		t.Fatal(err)
	}
	if d.Model != "ADATA SU650NS38" || d.Passed == nil || !*d.Passed || *d.TempC != 33 || *d.PowerOnHours != 12494 ||
		*d.Reallocated != 0 || *d.Pending != 2 || *d.Uncorrectable != 1 || d.Note != "" {
		t.Fatalf("ATA: %+v", d)
	}

	nvme := `{"smartctl":{"exit_status":0},"model_name":"NVMe","smart_status":{"passed":false},"nvme_smart_health_information_log":{"media_errors":3}}`
	d, err = ParseSMART([]byte(nvme))
	if err != nil || d.Passed == nil || *d.Passed || *d.Uncorrectable != 3 || d.Reallocated != nil {
		t.Fatalf("NVMe: %+v %v", d, err)
	}

	asleep := `{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)","severity":"information"}]},"power_mode":{"name":"STANDBY"}}`
	d, err = ParseSMART([]byte(asleep))
	if err != nil || d.Passed != nil || d.Note != "en reposo: no se despierta para leerlo" {
		t.Fatalf("dormido: %+v %v", d, err)
	}
}

func TestContainerCPU(t *testing.T) {
	prev := ContainerCounters{CPUUsage: 1_000, SystemUsage: 100_000, OnlineCPUs: 4}
	cur := ContainerCounters{CPUUsage: 6_000, SystemUsage: 110_000, OnlineCPUs: 4}
	if got := ContainerCPU(prev, cur); math.Abs(got-200) > 1e-9 {
		t.Fatalf("= %v, quería 200 (dos núcleos)", got)
	}
	if got := ContainerCPU(cur, prev); got != 0 {
		t.Fatalf("contadores para atrás: %v", got)
	}
}

func TestShowInterface(t *testing.T) {
	for n, want := range map[string]bool{"enp10s0": true, "vmbr0": true, "wt0": true, "eth0": true, "lo": false,
		"veth1a2b": false, "tap100i0": false, "fwbr102i0": false, "docker0": false, "br-0f2a": false} {
		if ShowInterface(n) != want {
			t.Errorf("ShowInterface(%q) = %v", n, !want)
		}
	}
}

type fakeUnits []agent.Unit

func (f fakeUnits) Units(context.Context) ([]agent.Unit, error) { return f, nil }

type fakeStats [][]ContainerCounters

func (f *fakeStats) Stats(context.Context) ([]ContainerCounters, error) {
	s := (*f)[0]
	*f = (*f)[1:]
	return s, nil
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSample: dos muestras sobre un /proc falso. La primera no publica
// métricas; la segunda da las velocidades entre las dos.
func TestSample(t *testing.T) {
	proc, sys := t.TempDir(), t.TempDir()
	write(t, proc, "sys/kernel/hostname", "pve\n")
	write(t, proc, "sys/kernel/osrelease", "7.0.14-28-pve\n")
	write(t, proc, "cpuinfo", "processor : 0\nmodel name : CPU de prueba\nprocessor : 1\n")
	write(t, proc, "uptime", "100.5 10\n")
	write(t, proc, "loadavg", "0.5 0.4 0.3 1/100 5\n")
	write(t, proc, "meminfo", "MemTotal: 1000 kB\nMemAvailable: 250 kB\nSwapTotal: 100 kB\nSwapFree: 100 kB\n")
	write(t, proc, "self/mounts", "")
	netdev := func(rx, tx int) string {
		return "h\nh\n  lo: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\nenp1s0: " + itoa(rx) + " 0 0 0 0 0 0 0 " + itoa(tx) + " 0 0 0 0 0 0 0\nveth1: 9 0 0 0 0 0 0 0 9 0 0 0 0 0 0 0\n"
	}
	disk := func(r, w int) string {
		return "8 0 sda 0 0 " + itoa(r) + " 0 0 0 " + itoa(w) + " 0 0 0 0\n8 1 sda1 0 0 1 0 0 0 1 0 0 0 0\n"
	}
	write(t, proc, "stat", "cpu 100 0 0 900 0 0 0 0\ncpu0 1 1 1 1\ncpu1 1 1 1 1\n")
	write(t, proc, "net/dev", netdev(1000, 0))
	write(t, proc, "diskstats", disk(0, 0))

	stats := &fakeStats{
		{{ID: "a", Name: "web", CPUUsage: 0, SystemUsage: 1000, OnlineCPUs: 2, Mem: 50, NetRx: 0}},
		{{ID: "a", Name: "web", CPUUsage: 250, SystemUsage: 2000, OnlineCPUs: 2, Mem: 60, NetRx: 1500}},
	}
	c := New(Options{Interval: 15 * time.Second, Proc: proc, Sys: sys, Docker: stats, Units: fakeUnits{{Name: "a.service", Active: "active"}}})
	t0 := time.Unix(1_800_000_000, 0)
	c.sample(context.Background(), t0)
	if s := c.System(); !s.At.IsZero() || s.Metrics != nil || len(s.Units) != 1 || s.Host.Hostname != "pve" || s.Host.Threads != 2 || !s.Docker {
		t.Fatalf("primera muestra: %+v", s)
	}

	write(t, proc, "stat", "cpu 125 0 0 975 0 0 0 0\n")
	write(t, proc, "net/dev", netdev(16000, 1500))
	write(t, proc, "diskstats", disk(30, 60)) // sectores de 512
	t1 := t0.Add(15 * time.Second)
	c.sample(context.Background(), t1)
	s := c.System()
	if !s.At.Equal(t1) || s.Metrics == nil {
		t.Fatalf("segunda muestra: %+v", s)
	}
	m := s.Metrics
	if m.CPU != 25 || m.MemTotal != 1000<<10 || m.MemUsed != 750<<10 || m.Load != [3]float64{0.5, 0.4, 0.3} {
		t.Errorf("host: %+v", m)
	}
	if len(m.Net) != 1 || m.Net[0] != (agent.NetRate{Name: "enp1s0", Rx: 1000, Tx: 100}) {
		t.Errorf("red: %+v", m.Net)
	}
	if len(m.Disks) != 1 || m.Disks[0] != (agent.DiskRate{Name: "sda", Read: 1024, Write: 2048}) {
		t.Errorf("discos: %+v", m.Disks)
	}
	if len(s.Containers) != 1 || s.Containers[0].CPU != 50 || s.Containers[0].Mem != 60 || s.Containers[0].NetRx != 100 {
		t.Errorf("contenedores: %+v", s.Containers)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
