// Package procfs lee los archivos de /proc y /sys que usa el colector de
// hardware del agent: CPU, memoria, red, discos, montajes y temperaturas.
//
// Son funciones puras sobre el contenido de cada archivo (un io.Reader, un
// string o un fs.FS): no abren rutas fijas, así se prueban con texto
// inventado y el agent decide de dónde leer (en un contenedor, /proc es el
// del host para estos archivos; /sys se monta aparte).
package procfs

import (
	"errors"
	"io"
	"io/fs"
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
func (t CPUTimes) Total() uint64 { panic("sin implementar") }

// Busy es Total menos Idle e IOWait.
func (t CPUTimes) Busy() uint64 { panic("sin implementar") }

// ParseStat lee /proc/stat: los tiempos de la línea "cpu" y cuántas líneas
// "cpuN" hay (los hilos). Una línea "cpu" con menos de ocho números (los
// kernels viejos traen cuatro o siete) completa con ceros lo que falta; con
// menos de cuatro, o sin línea "cpu", es ErrFormat.
func ParseStat(r io.Reader) (CPUTimes, int, error) { panic("sin implementar") }

// CPUPercent es el uso de CPU entre dos lecturas: 100 * Δbusy / Δtotal.
// Da 0 si Δtotal es 0 o si algún contador retrocedió (Total o Busy de cur
// menor que el de prev: el host reinició). El resultado queda entre 0 y 100.
func CPUPercent(prev, cur CPUTimes) float64 { panic("sin implementar") }

// Memory es /proc/meminfo, en bytes.
type Memory struct {
	Total, Available, Free, Buffers, Cached, SwapTotal, SwapFree uint64
}

// Used es Total menos Available (0 si Available es mayor).
func (m Memory) Used() uint64 { panic("sin implementar") }

// SwapUsed es SwapTotal menos SwapFree (0 si SwapFree es mayor).
func (m Memory) SwapUsed() uint64 { panic("sin implementar") }

// ParseMeminfo lee /proc/meminfo. Los valores vienen en kB (que en
// /proc/meminfo son KiB: se multiplican por 1024). Sin MemTotal es
// ErrFormat. Sin MemAvailable (kernels anteriores a 3.14), Available es
// Free + Buffers + Cached. Las claves que no son de Memory se ignoran.
func ParseMeminfo(r io.Reader) (Memory, error) { panic("sin implementar") }

// NetDev son los contadores de una interfaz de /proc/net/dev.
type NetDev struct {
	Name             string
	RxBytes, TxBytes uint64
}

// ParseNetDev lee /proc/net/dev: salta las dos líneas de encabezado y da
// las interfaces en el orden del archivo. El nombre va sin el ":" y sin
// espacios; ojo que entre el ":" y el primer número puede no haber espacio
// ("eth0:123 …"). Una línea con menos de 16 números es ErrFormat.
func ParseNetDev(r io.Reader) ([]NetDev, error) { panic("sin implementar") }

// DiskStat son los contadores de un dispositivo de /proc/diskstats, en
// bytes (los sectores de diskstats son siempre de 512 bytes).
type DiskStat struct {
	Name                  string
	ReadBytes, WriteBytes uint64
}

// ParseDiskstats lee /proc/diskstats en el orden del archivo. Columnas
// (desde 1): 3 nombre, 6 sectores leídos, 10 sectores escritos. Una línea
// con menos de 14 columnas es ErrFormat.
func ParseDiskstats(r io.Reader) ([]DiskStat, error) { panic("sin implementar") }

// WholeDisk dice si un nombre de /proc/diskstats es un disco entero y no
// una partición ni un dispositivo virtual. Son discos: sd<letras>,
// vd<letras>, xvd<letras>, hd<letras> (sin números después), nvme<N>n<M>
// (sin "p<K>") y mmcblk<N> (sin "p<K>"). Todo lo demás no: loop, ram, zram,
// dm-, md, sr, nbd y las particiones (sda1, nvme0n1p2, mmcblk0p1).
func WholeDisk(name string) bool { panic("sin implementar") }

// Mount es una línea de /proc/self/mounts.
type Mount struct {
	Device, Point, FSType string
}

// ParseMounts lee /proc/self/mounts (o /proc/mounts). Device y Point
// decodifican los escapes octales del kernel (\040 es espacio, \011 tab,
// \012 salto de línea, \134 barra invertida). Una línea con menos de tres
// campos es ErrFormat.
func ParseMounts(r io.Reader) ([]Mount, error) { panic("sin implementar") }

// RealFS dice si un tipo de sistema de archivos guarda datos en un disco:
// ext2, ext3, ext4, xfs, btrfs, zfs, f2fs, vfat, exfat, ntfs, ntfs3,
// fuseblk, jfs, reiserfs y bcachefs. Todo lo demás (tmpfs, overlay, proc,
// nfs, cifs…) no.
func RealFS(fstype string) bool { panic("sin implementar") }

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
func ReadHwmon(fsys fs.FS) ([]Temp, error) { panic("sin implementar") }

// ParseLoadavg lee /proc/loadavg: las cargas de 1, 5 y 15 minutos.
func ParseLoadavg(s string) (l1, l5, l15 float64, err error) { panic("sin implementar") }

// ParseUptime lee /proc/uptime: el primer número, en segundos con
// decimales.
func ParseUptime(s string) (time.Duration, error) { panic("sin implementar") }

// ParseCPUInfo lee /proc/cpuinfo: el primer "model name" (sin espacios de
// más) y cuántas líneas "processor" hay. Sin "model name" (ARM), el modelo
// es "" y no es error.
func ParseCPUInfo(r io.Reader) (model string, threads int, err error) { panic("sin implementar") }

// Rate es la velocidad entre dos lecturas de un contador, en unidades por
// segundo. Da 0 si elapsed no es positivo o si cur es menor que prev (el
// contador volvió a cero: la interfaz se recreó o el host reinició).
func Rate(prev, cur uint64, elapsed time.Duration) float64 { panic("sin implementar") }
