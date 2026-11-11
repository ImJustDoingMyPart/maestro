package agent

import (
	"context"
	"net/http"
	"time"
)

// System es lo que el agent sabe de su host además de los contenedores:
// métricas del host y de cada contenedor, unidades de systemd y, en un
// host de Proxmox, sus LXC y VMs. Es el cuerpo de GET /v1/system.
//
// Lo arma un colector que muestrea cada tanto (15 s): las velocidades (CPU,
// red, disco) son entre las dos últimas muestras. At es la hora de la
// última muestra; cero mientras no haya dos, porque una sola no da
// velocidades. El hub guarda una muestra por At, así que pedir dos veces
// la misma no la duplica.
type System struct {
	At   time.Time `json:"at,omitzero"`
	Host HostInfo  `json:"host"`
	// Metrics es nil hasta la segunda muestra.
	Metrics    *HostMetrics     `json:"metrics,omitempty"`
	Containers []ContainerStats `json:"containers,omitempty"`
	// Units es nil si el agent no lee systemd (sin --systemd o sin acceso
	// al bus); vacío si lo lee y no hay unidades.
	Units []Unit `json:"units,omitempty"`
	// Guests es nil si el host no es un Proxmox (sin --proxmox).
	Guests []Guest `json:"guests,omitempty"`
	// SMART es nil sin --smart. Se lee cada tanto (10 min), no en cada
	// muestra.
	SMART []SMARTDisk `json:"smart,omitempty"`
	// Docker dice si el agent lee Docker. Sin Docker (una LXC, el host de
	// Proxmox), /v1/containers da una lista vacía.
	Docker bool `json:"docker"`
	// Errors son los colectores que fallaron en la última muestra, con el
	// motivo ("systemd: …"). Lo demás de la muestra vale igual.
	Errors []string `json:"errors,omitempty"`
}

// HostInfo es lo que no cambia (o casi) de un host.
type HostInfo struct {
	Hostname string  `json:"hostname"`
	Kernel   string  `json:"kernel"`
	CPUModel string  `json:"cpu_model"`
	Threads  int     `json:"threads"`
	UptimeS  float64 `json:"uptime_s"`
}

// HostMetrics son las métricas de una muestra del host.
type HostMetrics struct {
	CPU       float64     `json:"cpu"` // porcentaje del total de núcleos, 0 a 100
	Load      [3]float64  `json:"load"`
	MemTotal  uint64      `json:"mem_total"` // bytes
	MemUsed   uint64      `json:"mem_used"`  // total menos disponible
	SwapTotal uint64      `json:"swap_total"`
	SwapUsed  uint64      `json:"swap_used"`
	Net       []NetRate   `json:"net,omitempty"`
	Disks     []DiskRate  `json:"disks,omitempty"`
	FS        []FSUsage   `json:"fs,omitempty"`
	Temps     []TempValue `json:"temps,omitempty"`
}

// NetRate es la velocidad de una interfaz, en bytes por segundo.
type NetRate struct {
	Name string  `json:"name"`
	Rx   float64 `json:"rx"`
	Tx   float64 `json:"tx"`
}

// DiskRate es la velocidad de un disco entero, en bytes por segundo.
type DiskRate struct {
	Name  string  `json:"name"`
	Read  float64 `json:"read"`
	Write float64 `json:"write"`
}

// FSUsage es el espacio de un sistema de archivos, en bytes. Used es Total
// menos lo libre (incluido lo reservado para root, como df).
type FSUsage struct {
	Mount  string `json:"mount"` // punto de montaje en el host
	Device string `json:"device"`
	FSType string `json:"fstype"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
}

// TempValue es un sensor de temperatura.
type TempValue struct {
	Chip    string  `json:"chip"`
	Label   string  `json:"label"`
	Celsius float64 `json:"celsius"`
}

// ContainerStats son las métricas de un contenedor que corre. Las
// velocidades son en bytes por segundo; CPU es porcentaje de un núcleo
// (200 son dos núcleos llenos, como docker stats).
type ContainerStats struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CPU      float64 `json:"cpu"`
	Mem      uint64  `json:"mem"`       // bytes, sin la caché de archivos (como docker stats)
	MemLimit uint64  `json:"mem_limit"` // 0 si no tiene límite propio
	NetRx    float64 `json:"net_rx"`
	NetTx    float64 `json:"net_tx"`
	Read     float64 `json:"read"`
	Write    float64 `json:"write"`
}

// Unit es una unidad de systemd: un servicio o un timer.
type Unit struct {
	Name        string `json:"name"` // "restic-backup.timer"
	Description string `json:"description"`
	Active      string `json:"active"` // ActiveState: active, inactive, failed, activating…
	Sub         string `json:"sub"`    // SubState: running, exited, dead, waiting…
	// Result es el Result de la unidad: "success" o el motivo de la última
	// falla ("exit-code", "signal", "timeout"…). En un timer es el del
	// servicio que dispara, que es lo que importa.
	Result string `json:"result,omitempty"`
	// ExitStatus es el código de salida del proceso principal (en un
	// timer, del servicio que dispara).
	ExitStatus int `json:"exit_status,omitempty"`
	// Timers: LastRun es cuándo disparó por última vez y NextRun cuándo
	// dispara la próxima (cero si no se sabe). Triggers es el servicio que
	// dispara.
	LastRun  time.Time `json:"last_run,omitzero"`
	NextRun  time.Time `json:"next_run,omitzero"`
	Triggers string    `json:"triggers,omitempty"`
}

// Guest es una LXC o una VM de un host de Proxmox.
type Guest struct {
	VMID    int     `json:"vmid"`
	Kind    string  `json:"kind"` // "lxc" o "vm"
	Name    string  `json:"name"`
	Status  string  `json:"status"` // "running", "stopped", "paused"…
	CPU     float64 `json:"cpu"`    // porcentaje de sus núcleos asignados, 0 a 100
	CPUs    int     `json:"cpus"`
	Mem     uint64  `json:"mem"`
	MaxMem  uint64  `json:"max_mem"`
	UptimeS float64 `json:"uptime_s"`
}

// SMARTDisk es el estado SMART de un disco, según smartctl.
type SMARTDisk struct {
	Device string `json:"device"` // "/dev/sda"
	Model  string `json:"model,omitempty"`
	Serial string `json:"serial,omitempty"`
	// Passed es el veredicto de la autoevaluación; nil si no se pudo leer
	// (disco dormido, sin soporte).
	Passed       *bool    `json:"passed,omitempty"`
	TempC        *float64 `json:"temp_c,omitempty"`
	PowerOnHours *int64   `json:"power_on_hours,omitempty"`
	// Reallocated, Pending y Uncorrectable son los atributos 5, 197 y 198
	// de ATA (sectores reasignados, pendientes e incorregibles); en NVMe,
	// MediaErrors va en Uncorrectable.
	Reallocated   *int64 `json:"reallocated,omitempty"`
	Pending       *int64 `json:"pending,omitempty"`
	Uncorrectable *int64 `json:"uncorrectable,omitempty"`
	// Note explica por qué falta algo ("en reposo: no se despierta para
	// leerlo").
	Note string    `json:"note,omitempty"`
	At   time.Time `json:"at"` // cuándo se leyó
}

// SystemSource da la última muestra del host. Lo implementa el colector.
type SystemSource interface {
	System() System
}

func handleSystem(src SystemSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, src.System())
	}
}

// System pide la última muestra del host.
func (c *Client) System(ctx context.Context) (System, error) {
	var out System
	err := c.get(ctx, "/v1/system", &out)
	return out, err
}
