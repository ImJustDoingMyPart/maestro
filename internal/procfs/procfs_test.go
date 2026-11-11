package procfs

import (
	"errors"
	"io/fs"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestParseStat(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    CPUTimes
		threads int
		err     bool
	}{
		{
			name: "kernel actual",
			in: "cpu  871521 111181 77718 8576862 5690 21131 9585 0 0 0\n" +
				"cpu0 66100 2041 5114 733532 312 1055 545 0 0 0\n" +
				"cpu1 68117 15413 7399 709214 586 4898 6196 0 0 0\n" +
				"intr 123 4 5\nctxt 999\nbtime 1700000000\n",
			want:    CPUTimes{User: 871521, Nice: 111181, System: 77718, Idle: 8576862, IOWait: 5690, IRQ: 21131, SoftIRQ: 9585},
			threads: 2,
		},
		{
			name:    "con steal",
			in:      "cpu 10 20 30 40 50 60 70 80 90 100\ncpu0 10 20 30 40 50 60 70 80 90 100\n",
			want:    CPUTimes{10, 20, 30, 40, 50, 60, 70, 80},
			threads: 1,
		},
		{
			name: "kernel viejo, cuatro campos",
			in:   "cpu 1 2 3 4\n",
			want: CPUTimes{User: 1, Nice: 2, System: 3, Idle: 4},
		},
		{
			name: "cpu10 cuenta como hilo",
			in:   "cpu 1 1 1 1 1 1 1 1\ncpu0 1 1 1 1\ncpu10 1 1 1 1\n",
			want: CPUTimes{1, 1, 1, 1, 1, 1, 1, 1}, threads: 2,
		},
		{name: "sin línea cpu", in: "cpu0 1 2 3 4\nintr 1\n", err: true},
		{name: "tres campos", in: "cpu 1 2 3\n", err: true},
		{name: "no es número", in: "cpu 1 2 x 4\n", err: true},
		{name: "vacío", in: "", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, threads, err := ParseStat(strings.NewReader(tt.in))
			if tt.err {
				if !errors.Is(err, ErrFormat) {
					t.Fatalf("err = %v, quería ErrFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || threads != tt.threads {
				t.Fatalf("= %+v, %d; quería %+v, %d", got, threads, tt.want, tt.threads)
			}
		})
	}
}

func TestCPUTimes(t *testing.T) {
	c := CPUTimes{User: 1, Nice: 2, System: 3, Idle: 4, IOWait: 5, IRQ: 6, SoftIRQ: 7, Steal: 8}
	if c.Total() != 36 || c.Busy() != 27 {
		t.Fatalf("Total %d Busy %d, quería 36 y 27", c.Total(), c.Busy())
	}
}

func TestCPUPercent(t *testing.T) {
	base := CPUTimes{User: 100, Idle: 900}
	tests := []struct {
		name      string
		prev, cur CPUTimes
		want      float64
	}{
		{"cuarto", base, CPUTimes{User: 125, Idle: 975}, 25},
		{"todo ocupado", base, CPUTimes{User: 200, Idle: 900}, 100},
		{"ocioso", base, CPUTimes{User: 100, Idle: 1000}, 0},
		{"iowait no es uso", base, CPUTimes{User: 150, Idle: 900, IOWait: 50}, 50},
		{"sin avance", base, base, 0},
		{"reinició", base, CPUTimes{User: 10, Idle: 20}, 0},
		{"busy retrocede", CPUTimes{User: 100, Idle: 900}, CPUTimes{User: 90, Idle: 1100}, 0},
		{"desde cero", CPUTimes{}, CPUTimes{User: 1, System: 1, Idle: 2}, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CPUPercent(tt.prev, tt.cur); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("= %v, quería %v", got, tt.want)
			}
		})
	}
}

func TestParseMeminfo(t *testing.T) {
	full := "MemTotal:       1000 kB\nMemFree:         100 kB\nMemAvailable:    600 kB\n" +
		"Buffers:           10 kB\nCached:           200 kB\nSwapCached: 0 kB\nSwapTotal:      512 kB\nSwapFree:        500 kB\nHugePages_Total: 0\n"
	tests := []struct {
		name string
		in   string
		want Memory
		err  bool
	}{
		{
			name: "completo",
			in:   full,
			want: Memory{Total: 1000 << 10, Available: 600 << 10, Free: 100 << 10, Buffers: 10 << 10, Cached: 200 << 10, SwapTotal: 512 << 10, SwapFree: 500 << 10},
		},
		{
			name: "sin MemAvailable",
			in:   "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 10 kB\nCached: 200 kB\n",
			want: Memory{Total: 1000 << 10, Available: 310 << 10, Free: 100 << 10, Buffers: 10 << 10, Cached: 200 << 10},
		},
		{name: "sin MemTotal", in: "MemFree: 100 kB\n", err: true},
		{name: "número roto", in: "MemTotal: x kB\n", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMeminfo(strings.NewReader(tt.in))
			if tt.err {
				if !errors.Is(err, ErrFormat) {
					t.Fatalf("err = %v, quería ErrFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("= %+v\nquería %+v", got, tt.want)
			}
		})
	}
}

func TestMemoryUsed(t *testing.T) {
	m := Memory{Total: 100, Available: 30, SwapTotal: 50, SwapFree: 20}
	if m.Used() != 70 || m.SwapUsed() != 30 {
		t.Fatalf("Used %d SwapUsed %d", m.Used(), m.SwapUsed())
	}
	odd := Memory{Total: 10, Available: 20, SwapTotal: 5, SwapFree: 6}
	if odd.Used() != 0 || odd.SwapUsed() != 0 {
		t.Fatalf("Used %d SwapUsed %d, quería 0 y 0", odd.Used(), odd.SwapUsed())
	}
}

func TestParseNetDev(t *testing.T) {
	in := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 40487887  213576    0    0    0     0          0         0 40487887  213576    0    0    0     0       0          0\n" +
		"enp10s0: 3453276970 2443689    0  380    0     0          0      6094 157390631  294947    0    0    0     0       0          0\n" +
		"  eth0:123 1 0 0 0 0 0 0 456 2 0 0 0 0 0 0\n"
	got, err := ParseNetDev(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []NetDev{{"lo", 40487887, 40487887}, {"enp10s0", 3453276970, 157390631}, {"eth0", 123, 456}}
	if !slices.Equal(got, want) {
		t.Fatalf("= %+v\nquería %+v", got, want)
	}

	if _, err := ParseNetDev(strings.NewReader("h\nh\neth0: 1 2 3\n")); !errors.Is(err, ErrFormat) {
		t.Fatalf("línea corta: err = %v, quería ErrFormat", err)
	}
	if got, err := ParseNetDev(strings.NewReader("h\nh\n")); err != nil || len(got) != 0 {
		t.Fatalf("sin interfaces: %v, %v", got, err)
	}
}

func TestParseDiskstats(t *testing.T) {
	in := " 259       0 nvme0n1 349870 13866 50985153 211113 686691 11141 40451561 733325 0 84711 949511 10338 0 28461040 3343 10195 1729\n" +
		"   8       1 sda1 10 0 4 0 0 0 2 0 0 0 0\n" +
		"   7       0 loop0 1 0 8 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
	got, err := ParseDiskstats(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []DiskStat{{"nvme0n1", 50985153 * 512, 40451561 * 512}, {"sda1", 4 * 512, 2 * 512}, {"loop0", 8 * 512, 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("= %+v\nquería %+v", got, want)
	}
	if _, err := ParseDiskstats(strings.NewReader("8 0 sda 1 2 3\n")); !errors.Is(err, ErrFormat) {
		t.Fatalf("línea corta: err = %v, quería ErrFormat", err)
	}
}

func TestWholeDisk(t *testing.T) {
	yes := []string{"sda", "sdb", "sdaa", "vda", "xvdb", "hda", "nvme0n1", "nvme12n3", "mmcblk0", "mmcblk10"}
	no := []string{"sda1", "sdb12", "vda2", "nvme0n1p1", "nvme0", "mmcblk0p1", "mmcblk", "loop0", "ram0", "zram0",
		"dm-0", "md0", "md127", "sr0", "nbd0", "sd", "", "xvd1", "nvmen1"}
	for _, n := range yes {
		if !WholeDisk(n) {
			t.Errorf("WholeDisk(%q) = false, quería true", n)
		}
	}
	for _, n := range no {
		if WholeDisk(n) {
			t.Errorf("WholeDisk(%q) = true, quería false", n)
		}
	}
}

func TestParseMounts(t *testing.T) {
	in := "/dev/nvme0n1p2 / btrfs rw,noatime 0 0\n" +
		"tmpfs /dev/shm tmpfs rw 0 0\n" +
		"/dev/sdb1 /mnt/mis\\040juegos ext4 rw 0 0\n" +
		"//nas/a\\134b /mnt/tab\\011x cifs rw 0 0\n"
	got, err := ParseMounts(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{
		{"/dev/nvme0n1p2", "/", "btrfs"},
		{"tmpfs", "/dev/shm", "tmpfs"},
		{"/dev/sdb1", "/mnt/mis juegos", "ext4"},
		{`//nas/a\b`, "/mnt/tab\tx", "cifs"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("= %q\nquería %q", got, want)
	}
	if _, err := ParseMounts(strings.NewReader("/dev/sda1 /\n")); !errors.Is(err, ErrFormat) {
		t.Fatalf("línea corta: err = %v, quería ErrFormat", err)
	}
}

func TestRealFS(t *testing.T) {
	for _, f := range []string{"ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "vfat", "exfat", "ntfs", "ntfs3", "fuseblk", "jfs", "reiserfs", "bcachefs"} {
		if !RealFS(f) {
			t.Errorf("RealFS(%q) = false", f)
		}
	}
	for _, f := range []string{"tmpfs", "overlay", "proc", "sysfs", "nfs", "nfs4", "cifs", "devtmpfs", "cgroup2", "fuse.rclone", "", "EXT4"} {
		if RealFS(f) {
			t.Errorf("RealFS(%q) = true", f)
		}
	}
}

func TestReadHwmon(t *testing.T) {
	fsys := fstest.MapFS{
		"hwmon0/name":         {Data: []byte("acpitz\n")},
		"hwmon0/temp1_input":  {Data: []byte("27800\n")},
		"hwmon10/name":        {Data: []byte("nvme\n")},
		"hwmon10/temp1_input": {Data: []byte("41850\n")},
		"hwmon10/temp1_label": {Data: []byte("Composite\n")},
		"hwmon2/name":         {Data: []byte("coretemp\n")},
		"hwmon2/temp10_input": {Data: []byte("50000\n")},
		"hwmon2/temp2_input":  {Data: []byte("45000\n")},
		"hwmon2/temp2_label":  {Data: []byte("Core 0\n")},
		"hwmon2/temp1_input":  {Data: []byte("-1500\n")},
		"hwmon2/temp1_label":  {Data: []byte("Package id 0\n")},
		"hwmon2/temp3_input":  {Data: []byte("apagado\n")},
		"hwmon2/temp1_max":    {Data: []byte("100000\n")},
		"hwmon2/fan1_input":   {Data: []byte("1200\n")},
		"hwmon3/temp1_input":  {Data: []byte("30000\n")},
		"hwmon4/name":         {Data: []byte("sin sensores\n")},
		"otra/name":           {Data: []byte("no\n")},
		"otra/temp1_input":    {Data: []byte("1000\n")},
	}
	got, err := ReadHwmon(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []Temp{
		{"acpitz", "temp1", 27.8},
		{"coretemp", "Package id 0", -1.5},
		{"coretemp", "Core 0", 45},
		{"coretemp", "temp10", 50},
		{"hwmon3", "temp1", 30},
		{"nvme", "Composite", 41.85},
	}
	if len(got) != len(want) {
		t.Fatalf("= %+v\nquería %+v", got, want)
	}
	for i := range want {
		if got[i].Chip != want[i].Chip || got[i].Label != want[i].Label || math.Abs(got[i].Celsius-want[i].Celsius) > 1e-9 {
			t.Fatalf("[%d] = %+v, quería %+v\ntodo: %+v", i, got[i], want[i], got)
		}
	}

	// En /sys/class/hwmon, hwmonN es un symlink a la carpeta del
	// dispositivo.
	linked := fstest.MapFS{
		"hwmon0": {Data: []byte("devices/platform/coretemp.0/hwmon/hwmon0"), Mode: fs.ModeSymlink},
		"devices/platform/coretemp.0/hwmon/hwmon0/name":        {Data: []byte("coretemp\n")},
		"devices/platform/coretemp.0/hwmon/hwmon0/temp1_input": {Data: []byte("42000\n")},
	}
	if got, err := ReadHwmon(linked); err != nil || len(got) != 1 || got[0].Chip != "coretemp" || got[0].Celsius != 42 {
		t.Fatalf("symlink: %+v, %v", got, err)
	}

	if got, err := ReadHwmon(fstest.MapFS{}); err != nil || got != nil {
		t.Fatalf("vacío: %v, %v; quería nil, nil", got, err)
	}
}

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg("0.27 0.63 2.44 2/1436 114827\n")
	if err != nil || l1 != 0.27 || l5 != 0.63 || l15 != 2.44 {
		t.Fatalf("= %v %v %v %v", l1, l5, l15, err)
	}
	if _, _, _, err := ParseLoadavg("0.27 x"); err == nil {
		t.Fatal("quería error")
	}
}

func TestParseUptime(t *testing.T) {
	d, err := ParseUptime("8201.77 85768.64\n")
	if err != nil || d != 8201770*time.Millisecond {
		t.Fatalf("= %v, %v", d, err)
	}
	if _, err := ParseUptime(""); err == nil {
		t.Fatal("quería error")
	}
}

func TestParseCPUInfo(t *testing.T) {
	in := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM) i5-10400F CPU @ 2.90GHz  \n\n" +
		"processor\t: 1\nmodel name\t: otro\n\n"
	model, threads, err := ParseCPUInfo(strings.NewReader(in))
	if err != nil || model != "Intel(R) Core(TM) i5-10400F CPU @ 2.90GHz" || threads != 2 {
		t.Fatalf("= %q %d %v", model, threads, err)
	}
	model, threads, err = ParseCPUInfo(strings.NewReader("processor : 0\nBogoMIPS : 50\n"))
	if err != nil || model != "" || threads != 1 {
		t.Fatalf("ARM: = %q %d %v", model, threads, err)
	}
}

func TestRate(t *testing.T) {
	tests := []struct {
		prev, cur uint64
		el        time.Duration
		want      float64
	}{
		{100, 1100, 10 * time.Second, 100},
		{100, 100, time.Second, 0},
		{100, 50, time.Second, 0},
		{0, 500, 500 * time.Millisecond, 1000},
		{0, 10, 0, 0},
		{0, 10, -time.Second, 0},
	}
	for _, tt := range tests {
		if got := Rate(tt.prev, tt.cur, tt.el); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("Rate(%d, %d, %v) = %v, quería %v", tt.prev, tt.cur, tt.el, got, tt.want)
		}
	}
}
