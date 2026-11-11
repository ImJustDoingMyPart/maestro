// Package procfs lee los archivos de /proc y /sys que usa el colector de
// hardware del agent: CPU, memoria, red, discos, montajes y temperaturas.
//
// Son funciones puras sobre el contenido de cada archivo (un io.Reader, un
// string o un fs.FS): no abren rutas fijas, así se prueban con texto
// inventado y el agent decide de dónde leer (en un contenedor, /proc es el
// del host para estos archivos; /sys se monta aparte).
package procfs

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrFormat es un archivo que no tiene el formato esperado.
var ErrFormat = errors.New("procfs: formato inesperado")

// CPUTimes es la línea "cpu" (la suma de todos los núcleos) de /proc/stat,
// en ticks de USER_HZ. Los campos guest y guest_nice ya están sumados en
// user y nice por el kernel, así que no se leen.
type CPUTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// Total es la suma de los ocho campos.
func (t CPUTimes) Total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// Busy es Total menos Idle e IOWait.
func (t CPUTimes) Busy() uint64 {
	return t.Total() - t.Idle - t.IOWait
}

// ParseStat lee /proc/stat: los tiempos de la línea "cpu" y cuántas líneas
// "cpuN" hay (los hilos). Una línea "cpu" con menos de ocho números (los
// kernels viejos traen cuatro o siete) completa con ceros lo que falta; con
// menos de cuatro, o sin línea "cpu", es ErrFormat.
func ParseStat(r io.Reader) (CPUTimes, int, error) {
	var (
		t       CPUTimes
		threads int
		found   bool
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			if isDigits(f[0][3:]) {
				threads++
			}
			continue
		}
		if len(f) < 5 {
			return CPUTimes{}, 0, fmt.Errorf("línea cpu con menos de cuatro números: %w", ErrFormat)
		}
		// Los campos que faltan quedan en cero.
		dst := [8]*uint64{&t.User, &t.Nice, &t.System, &t.Idle, &t.IOWait, &t.IRQ, &t.SoftIRQ, &t.Steal}
		for i, p := range dst {
			if i+1 >= len(f) {
				break
			}
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return CPUTimes{}, 0, fmt.Errorf("línea cpu: %w", ErrFormat)
			}
			*p = n
		}
		found = true
	}
	if err := sc.Err(); err != nil {
		return CPUTimes{}, 0, err
	}
	if !found {
		return CPUTimes{}, 0, fmt.Errorf("sin línea cpu: %w", ErrFormat)
	}
	return t, threads, nil
}

// CPUPercent es el uso de CPU entre dos lecturas: 100 * Δbusy / Δtotal.
// Da 0 si Δtotal es 0 o si algún contador retrocedió (Total o Busy de cur
// menor que el de prev: el host reinició). El resultado queda entre 0 y 100.
func CPUPercent(prev, cur CPUTimes) float64 {
	if cur.Total() < prev.Total() || cur.Busy() < prev.Busy() {
		return 0
	}
	dt := cur.Total() - prev.Total()
	if dt == 0 {
		return 0
	}
	return min(100, 100*float64(cur.Busy()-prev.Busy())/float64(dt))
}

// Memory es /proc/meminfo, en bytes.
type Memory struct {
	Total, Available, Free, Buffers, Cached, SwapTotal, SwapFree uint64
}

// Used es Total menos Available (0 si Available es mayor).
func (m Memory) Used() uint64 {
	return sub(m.Total, m.Available)
}

// SwapUsed es SwapTotal menos SwapFree (0 si SwapFree es mayor).
func (m Memory) SwapUsed() uint64 {
	return sub(m.SwapTotal, m.SwapFree)
}

// ParseMeminfo lee /proc/meminfo. Los valores vienen en kB (que en
// /proc/meminfo son KiB: se multiplican por 1024). Sin MemTotal es
// ErrFormat. Sin MemAvailable (kernels anteriores a 3.14), Available es
// Free + Buffers + Cached. Las claves que no son de Memory se ignoran.
func ParseMeminfo(r io.Reader) (Memory, error) {
	var m Memory
	fields := map[string]*uint64{
		"MemTotal": &m.Total, "MemAvailable": &m.Available, "MemFree": &m.Free,
		"Buffers": &m.Buffers, "Cached": &m.Cached,
		"SwapTotal": &m.SwapTotal, "SwapFree": &m.SwapFree,
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		dst := fields[key]
		if !ok || dst == nil {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return Memory{}, fmt.Errorf("%s sin valor: %w", key, ErrFormat)
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return Memory{}, fmt.Errorf("%s: %w", key, ErrFormat)
		}
		*dst = n << 10
		seen[key] = true
	}
	if err := sc.Err(); err != nil {
		return Memory{}, err
	}
	if !seen["MemTotal"] {
		return Memory{}, fmt.Errorf("sin MemTotal: %w", ErrFormat)
	}
	if !seen["MemAvailable"] {
		m.Available = m.Free + m.Buffers + m.Cached
	}
	return m, nil
}

// NetDev son los contadores de una interfaz de /proc/net/dev.
type NetDev struct {
	Name             string
	RxBytes, TxBytes uint64
}

// ParseNetDev lee /proc/net/dev: salta las dos líneas de encabezado y da
// las interfaces en el orden del archivo. El nombre va sin el ":" y sin
// espacios; ojo que entre el ":" y el primer número puede no haber espacio
// ("eth0:123 …"). Una línea con menos de 16 números es ErrFormat.
func ParseNetDev(r io.Reader) ([]NetDev, error) {
	var devs []NetDev
	sc := bufio.NewScanner(r)
	for i := 0; sc.Scan(); {
		if i < 2 {
			i++
			continue
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		f := strings.Fields(rest)
		if !ok || len(f) < 16 {
			return nil, fmt.Errorf("línea de interfaz corta: %w", ErrFormat)
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s: %w", name, ErrFormat)
		}
		devs = append(devs, NetDev{Name: strings.TrimSpace(name), RxBytes: rx, TxBytes: tx})
	}
	return devs, sc.Err()
}

// DiskStat son los contadores de un dispositivo de /proc/diskstats, en
// bytes (los sectores de diskstats son siempre de 512 bytes).
type DiskStat struct {
	Name                  string
	ReadBytes, WriteBytes uint64
}

// ParseDiskstats lee /proc/diskstats en el orden del archivo. Columnas
// (desde 1): 3 nombre, 6 sectores leídos, 10 sectores escritos. Una línea
// con menos de 14 columnas es ErrFormat.
func ParseDiskstats(r io.Reader) ([]DiskStat, error) {
	var disks []DiskStat
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 14 {
			return nil, fmt.Errorf("línea de diskstats con menos de 14 columnas: %w", ErrFormat)
		}
		rd, err1 := strconv.ParseUint(f[5], 10, 64)
		wr, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s: %w", f[2], ErrFormat)
		}
		disks = append(disks, DiskStat{Name: f[2], ReadBytes: rd * 512, WriteBytes: wr * 512})
	}
	return disks, sc.Err()
}

// WholeDisk dice si un nombre de /proc/diskstats es un disco entero y no
// una partición ni un dispositivo virtual. Son discos: sd<letras>,
// vd<letras>, xvd<letras>, hd<letras> (sin números después), nvme<N>n<M>
// (sin "p<K>") y mmcblk<N> (sin "p<K>"). Todo lo demás no: loop, ram, zram,
// dm-, md, sr, nbd y las particiones (sda1, nvme0n1p2, mmcblk0p1).
func WholeDisk(name string) bool {
	for _, p := range []string{"sd", "vd", "xvd", "hd"} {
		if rest, ok := strings.CutPrefix(name, p); ok {
			return rest != "" && !strings.ContainsFunc(rest, func(r rune) bool { return r < 'a' || r > 'z' })
		}
	}
	if rest, ok := strings.CutPrefix(name, "nvme"); ok {
		n, m, ok := strings.Cut(rest, "n")
		return ok && isDigits(n) && isDigits(m)
	}
	if rest, ok := strings.CutPrefix(name, "mmcblk"); ok {
		return isDigits(rest)
	}
	return false
}

// Mount es una línea de /proc/self/mounts.
type Mount struct {
	Device, Point, FSType string
}

// ParseMounts lee /proc/self/mounts (o /proc/mounts). Device y Point
// decodifican los escapes octales del kernel (\040 es espacio, \011 tab,
// \012 salto de línea, \134 barra invertida). Una línea con menos de tres
// campos es ErrFormat.
func ParseMounts(r io.Reader) ([]Mount, error) {
	var mounts []Mount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 {
			return nil, fmt.Errorf("línea de mounts con menos de tres campos: %w", ErrFormat)
		}
		mounts = append(mounts, Mount{Device: unescapeOctal(f[0]), Point: unescapeOctal(f[1]), FSType: f[2]})
	}
	return mounts, sc.Err()
}

// RealFS dice si un tipo de sistema de archivos guarda datos en un disco:
// ext2, ext3, ext4, xfs, btrfs, zfs, f2fs, vfat, exfat, ntfs, ntfs3,
// fuseblk, jfs, reiserfs y bcachefs. Todo lo demás (tmpfs, overlay, proc,
// nfs, cifs…) no.
func RealFS(fstype string) bool {
	switch fstype {
	case "ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "vfat", "exfat",
		"ntfs", "ntfs3", "fuseblk", "jfs", "reiserfs", "bcachefs":
		return true
	}
	return false
}

// Temp es un sensor de temperatura de hwmon.
type Temp struct {
	Chip    string // contenido de hwmonN/name, sin el salto de línea
	Label   string // contenido de tempM_label; si no existe, "tempM"
	Celsius float64
}

// ReadHwmon lee los sensores de temperatura de fsys, que es
// /sys/class/hwmon: carpetas hwmonN con un archivo name y archivos
// tempM_input (milésimas de grado) y, opcionalmente, tempM_label.
//
// Se ordenan por N y después por M, de menor a mayor como números (hwmon10
// va después de hwmon2). Una carpeta sin name usa la carpeta como Chip. Un
// tempM_input que no se puede leer o no es un entero se salta (los sensores
// apagados dan error al leerlos). Lo que no es una carpeta hwmonN se
// ignora. Sin sensores, nil y sin error; si fsys no se puede listar, el
// error.
func ReadHwmon(fsys fs.FS) ([]Temp, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	type chip struct {
		dir string
		n   int
	}
	var chips []chip
	for _, e := range entries {
		// En /sys/class/hwmon las entradas son symlinks a la carpeta del
		// dispositivo: se sigue el link para ver si es una carpeta.
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if fi, err := fs.Stat(fsys, e.Name()); err == nil {
				isDir = fi.IsDir()
			}
		}
		if rest, ok := strings.CutPrefix(e.Name(), "hwmon"); ok && isDir && isDigits(rest) {
			n, _ := strconv.Atoi(rest)
			chips = append(chips, chip{e.Name(), n})
		}
	}
	slices.SortFunc(chips, func(a, b chip) int { return cmp.Compare(a.n, b.n) })

	var temps []Temp
	for _, c := range chips {
		name := c.dir
		if b, err := fs.ReadFile(fsys, c.dir+"/name"); err == nil {
			name = strings.TrimSpace(string(b))
		}
		files, err := fs.ReadDir(fsys, c.dir)
		if err != nil {
			continue
		}
		var sensors []int
		for _, f := range files {
			if m, ok := strings.CutPrefix(f.Name(), "temp"); ok {
				if m, ok := strings.CutSuffix(m, "_input"); ok && isDigits(m) {
					n, _ := strconv.Atoi(m)
					sensors = append(sensors, n)
				}
			}
		}
		slices.Sort(sensors)
		for _, m := range sensors {
			prefix := fmt.Sprintf("%s/temp%d", c.dir, m)
			b, err := fs.ReadFile(fsys, prefix+"_input")
			if err != nil {
				continue
			}
			milli, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			if err != nil {
				continue
			}
			label := fmt.Sprintf("temp%d", m)
			if b, err := fs.ReadFile(fsys, prefix+"_label"); err == nil {
				label = strings.TrimSpace(string(b))
			}
			temps = append(temps, Temp{Chip: name, Label: label, Celsius: float64(milli) / 1000})
		}
	}
	return temps, nil
}

// ParseLoadavg lee /proc/loadavg: las cargas de 1, 5 y 15 minutos.
func ParseLoadavg(s string) (l1, l5, l15 float64, err error) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("loadavg con menos de tres campos: %w", ErrFormat)
	}
	var v [3]float64
	for i := range v {
		if v[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return 0, 0, 0, fmt.Errorf("loadavg: %w", ErrFormat)
		}
	}
	return v[0], v[1], v[2], nil
}

// ParseUptime lee /proc/uptime: el primer número, en segundos con
// decimales.
func ParseUptime(s string) (time.Duration, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("uptime vacío: %w", ErrFormat)
	}
	sec, err := strconv.ParseFloat(f[0], 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("uptime: %w", ErrFormat)
	}
	return time.Duration(math.Round(sec * float64(time.Second))), nil
}

// ParseCPUInfo lee /proc/cpuinfo: el primer "model name" (sin espacios de
// más) y cuántas líneas "processor" hay. Sin "model name" (ARM), el modelo
// es "" y no es error.
func ParseCPUInfo(r io.Reader) (model string, threads int, err error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "processor":
			threads++
		case "model name":
			if model == "" {
				model = strings.Join(strings.Fields(val), " ")
			}
		}
	}
	return model, threads, sc.Err()
}

// Rate es la velocidad entre dos lecturas de un contador, en unidades por
// segundo. Da 0 si elapsed no es positivo o si cur es menor que prev (el
// contador volvió a cero: la interfaz se recreó o el host reinició).
func Rate(prev, cur uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / elapsed.Seconds()
}

// sub es a - b, o 0 si b es mayor (los uint64 no admiten negativos).
func sub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// isDigits dice si s es una secuencia no vacía de dígitos ASCII.
func isDigits(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

// unescapeOctal decodifica los escapes \ooo (tres dígitos octales) que el
// kernel usa en /proc/self/mounts; lo que no tiene esa forma queda igual.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
