package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	collector "github.com/ImJustDoingMyPart/maestro/internal/collect"
)

// statsWorkers acota cuántos pedidos de stats van a la vez al daemon.
const statsWorkers = 8

// Stats lee los contadores de los contenedores que corren, uno por pedido
// "one-shot" (sin esperar la segunda muestra que Docker toma para el
// precpu: las velocidades las calcula el colector). Cumple
// collector.DockerStats.
func (c *Client) Stats(ctx context.Context) ([]collector.ContainerCounters, error) {
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listar contenedores: %w", err)
	}

	var (
		mu   sync.Mutex
		out  = make([]collector.ContainerCounters, 0, len(list.Items))
		errs []string
		wg   sync.WaitGroup
		sem  = make(chan struct{}, statsWorkers)
	)
	for _, s := range list.Items {
		name := ""
		if len(s.Names) > 0 {
			name = strings.TrimPrefix(s.Names[0], "/")
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			cc, err := c.oneStats(ctx, s.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case cerrdefs.IsNotFound(err):
				// se borró entre el list y el pedido
			case err != nil:
				errs = append(errs, name+": "+err.Error())
			default:
				cc.ID, cc.Name = s.ID, name
				out = append(out, cc)
			}
		})
	}
	wg.Wait()
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("stats: %s", errs[0])
	}
	return out, nil
}

func (c *Client) oneStats(ctx context.Context, id string) (collector.ContainerCounters, error) {
	res, err := c.api.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return collector.ContainerCounters{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return collector.ContainerCounters{}, fmt.Errorf("stats de %s: %w", id, err)
	}
	return countersFrom(st), nil
}

// countersFrom pasa la respuesta de la API a contadores. La memoria es la
// de docker stats: el uso menos la caché de archivos inactiva, que el
// kernel puede soltar cuando quiera.
func countersFrom(st container.StatsResponse) collector.ContainerCounters {
	cc := collector.ContainerCounters{
		CPUUsage:    st.CPUStats.CPUUsage.TotalUsage,
		SystemUsage: st.CPUStats.SystemUsage,
		OnlineCPUs:  int(st.CPUStats.OnlineCPUs),
		Mem:         st.MemoryStats.Usage,
		MemLimit:    st.MemoryStats.Limit,
	}
	if cc.OnlineCPUs == 0 {
		cc.OnlineCPUs = len(st.CPUStats.CPUUsage.PercpuUsage)
	}
	// cgroup v2 la llama inactive_file; v1, total_inactive_file.
	inactive, ok := st.MemoryStats.Stats["inactive_file"]
	if !ok {
		inactive = st.MemoryStats.Stats["total_inactive_file"]
	}
	if inactive < cc.Mem {
		cc.Mem -= inactive
	}
	for _, n := range st.Networks {
		cc.NetRx += n.RxBytes
		cc.NetTx += n.TxBytes
	}
	for _, e := range st.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			cc.Read += e.Value
		case "write":
			cc.Write += e.Value
		}
	}
	return cc
}
