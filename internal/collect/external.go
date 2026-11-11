package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// readGuests pide las LXC y VMs del nodo a pvesh. Solo anda en un host de
// Proxmox y como root.
func readGuests(ctx context.Context) ([]agent.Guest, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("pvesh: %w%s", err, stderrOf(err))
	}
	return ParseGuests(out)
}

func stderrOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return ": " + string(bytes.TrimSpace(ee.Stderr[:min(len(ee.Stderr), 300)]))
	}
	return ""
}

// ParseGuests lee la salida JSON de "pvesh get /cluster/resources --type
// vm". Los templates se saltan; los invitados se ordenan por VMID.
func ParseGuests(b []byte) ([]agent.Guest, error) {
	var raw []struct {
		VMID     int     `json:"vmid"`
		Type     string  `json:"type"` // "qemu" o "lxc"
		Name     string  `json:"name"`
		Status   string  `json:"status"`
		CPU      float64 `json:"cpu"` // fracción de maxcpu, 0 a 1
		MaxCPU   int     `json:"maxcpu"`
		Mem      uint64  `json:"mem"`
		MaxMem   uint64  `json:"maxmem"`
		Uptime   float64 `json:"uptime"`
		Template int     `json:"template"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("pvesh: salida inválida: %w", err)
	}
	out := make([]agent.Guest, 0, len(raw))
	for _, r := range raw {
		if r.Template == 1 {
			continue
		}
		kind := "vm"
		if r.Type == "lxc" {
			kind = "lxc"
		}
		out = append(out, agent.Guest{
			VMID: r.VMID, Kind: kind, Name: r.Name, Status: r.Status,
			CPU: r.CPU * 100, CPUs: r.MaxCPU, Mem: r.Mem, MaxMem: r.MaxMem, UptimeS: r.Uptime,
		})
	}
	slices.SortFunc(out, func(a, b agent.Guest) int { return a.VMID - b.VMID })
	return out, nil
}

// readSMART corre smartctl en cada disco entero de /sys/block. Con "-n
// standby" no despierta a un disco en reposo: un HDD que duerme sale con
// una nota y sin datos.
func readSMART(ctx context.Context, sys string, now time.Time) []agent.SMARTDisk {
	var out []agent.SMARTDisk
	for _, name := range wholeDisks(sys) {
		dev := "/dev/" + name
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		b, err := exec.CommandContext(cctx, "smartctl", "-n", "standby", "--json=c", "-a", dev).Output()
		cancel()
		// smartctl usa el código de salida como máscara de bits: los
		// bits altos son avisos (atributos vencidos, errores en el log),
		// no fallas de lectura. Si dio JSON, se lee.
		d, perr := ParseSMART(b)
		d.Device, d.At = dev, now
		if perr != nil {
			d.Note = "no se pudo leer"
			if err != nil {
				d.Note += ": " + err.Error()
			}
		}
		out = append(out, d)
	}
	return out
}

// ParseSMART lee la salida JSON de "smartctl --json -a". Device y At los
// pone quien llama.
func ParseSMART(b []byte) (agent.SMARTDisk, error) {
	var r struct {
		Smartctl struct {
			ExitStatus int `json:"exit_status"`
			Messages   []struct {
				String string `json:"string"`
			} `json:"messages"`
		} `json:"smartctl"`
		PowerMode *struct {
			Name string `json:"name"`
		} `json:"power_mode"`
		Model       string `json:"model_name"`
		Serial      string `json:"serial_number"`
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature *struct {
			Current float64 `json:"current"`
		} `json:"temperature"`
		PowerOnTime *struct {
			Hours int64 `json:"hours"`
		} `json:"power_on_time"`
		Attributes *struct {
			Table []struct {
				ID  int `json:"id"`
				Raw struct {
					Value int64 `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
		NVMe *struct {
			MediaErrors *int64 `json:"media_errors"`
		} `json:"nvme_smart_health_information_log"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return agent.SMARTDisk{}, err
	}
	d := agent.SMARTDisk{Model: r.Model, Serial: r.Serial}
	if r.SmartStatus != nil {
		p := r.SmartStatus.Passed
		d.Passed = &p
	}
	if r.Temperature != nil {
		t := r.Temperature.Current
		d.TempC = &t
	}
	if r.PowerOnTime != nil {
		h := r.PowerOnTime.Hours
		d.PowerOnHours = &h
	}
	if r.Attributes != nil {
		for _, a := range r.Attributes.Table {
			v := a.Raw.Value
			switch a.ID {
			case 5:
				d.Reallocated = &v
			case 197:
				d.Pending = &v
			case 198:
				d.Uncorrectable = &v
			}
		}
	}
	if r.NVMe != nil && r.NVMe.MediaErrors != nil {
		d.Uncorrectable = r.NVMe.MediaErrors
	}
	// Con -n standby, un disco dormido sale con el bit 1 (0x2) y un
	// mensaje; no hay veredicto ni atributos.
	if d.Passed == nil && r.Smartctl.ExitStatus&2 != 0 {
		d.Note = "en reposo: no se despierta para leerlo"
		for _, m := range r.Smartctl.Messages {
			if m.String != "" && !bytes.Contains([]byte(m.String), []byte("STANDBY")) {
				d.Note = m.String
			}
		}
	}
	return d, nil
}
