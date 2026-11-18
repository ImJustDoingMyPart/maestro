package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// Client lee Docker: contenedores, logs y la última salida de cada uno.
type Client struct {
	api *client.Client

	mu    sync.Mutex
	exits map[string]exitInfo // por ID de contenedor; lo llena WatchExits
}

// exitInfo es la última vez que terminó un contenedor. Docker borra el exit
// code del inspect cuando el contenedor vuelve a arrancar, así que la única
// forma de saber por qué reinició es haber visto el evento "die".
type exitInfo struct {
	code int
	at   time.Time
}

// New crea el cliente desde el entorno (DOCKER_HOST y compañía). La versión
// de la API se negocia sola con el daemon en la primera petición.
//
// El SDK no entiende DOCKER_HOST=ssh://: eso lo resuelve el CLI de Docker
// con un helper propio. Para desarrollar contra un server remoto se usa un
// túnel del socket (ver el README).
func New() (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{api: api, exits: make(map[string]exitInfo)}, nil
}

// Close libera las conexiones.
func (c *Client) Close() error { return c.api.Close() }

// Containers lista todos los contenedores, incluidos los detenidos. Los
// reinicios solo vienen en el inspect, así que hay una llamada por
// contenedor.
func (c *Client) Containers(ctx context.Context) ([]agent.Container, error) {
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listar contenedores: %w", err)
	}

	out := make([]agent.Container, 0, len(list.Items))
	for _, s := range list.Items {
		ins, err := c.api.ContainerInspect(ctx, s.ID, client.ContainerInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			continue // se borró entre el list y el inspect
		}
		if err != nil {
			return nil, fmt.Errorf("inspeccionar %s: %w", s.ID, err)
		}
		out = append(out, fromSummary(s, ins.Container.State, ins.Container.RestartCount))
	}

	// Suma la última salida vista y olvida la de los contenedores que ya
	// no existen.
	c.mu.Lock()
	defer c.mu.Unlock()
	alive := make(map[string]bool, len(out))
	for i := range out {
		alive[out[i].ID] = true
		if e, ok := c.exits[out[i].ID]; ok {
			code := e.code
			out[i].LastExitCode, out[i].LastExitAt = &code, e.at
		}
	}
	for id := range c.exits {
		if !alive[id] {
			delete(c.exits, id)
		}
	}
	return out, nil
}

// exitReplay es cuánto para atrás se le piden a Docker los eventos "die"
// al arrancar: el exit code de los reinicios vive en memoria del agent, y
// así un agent recién reiniciado recupera el de la última hora (Docker
// guarda los eventos recientes mientras el daemon no reinicie). Un
// reinicio cuenta como reciente durante 10 minutos en el hub.
const exitReplay = time.Hour

// WatchExits escucha los eventos "die" de Docker y guarda el exit code de
// cada contenedor, hasta que se cancele ctx. Al abrir el stream pide
// también los de la última hora (exitReplay). Si se corta (Docker
// reinició, se cayó el socket), lo vuelve a abrir con espera creciente,
// desde el último evento visto: no se pierde lo que pasó en el medio.
func (c *Client) WatchExits(ctx context.Context, logger *slog.Logger) {
	wait := time.Second
	since := time.Now().Add(-exitReplay)
	for {
		opened := time.Now()
		last, err := c.watchOnce(ctx, since)
		if !last.IsZero() {
			since = last
		}
		if ctx.Err() != nil {
			return
		}
		if time.Since(opened) > time.Minute {
			wait = time.Second // anduvo un rato: no era un error que se repite
		}
		logger.Warn("eventos de Docker cortados; se reabren", "err", err, "en", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

// watchOnce sigue el stream desde since hasta que se corta. Devuelve la
// hora del último evento que guardó (cero si ninguno).
func (c *Client) watchOnce(ctx context.Context, since time.Time) (time.Time, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // cierra el stream si se sale por un error
	res := c.api.Events(ctx, client.EventsListOptions{
		Since:   strconv.FormatInt(since.Unix(), 10),
		Filters: make(client.Filters).Add("type", "container").Add("event", "die"),
	})
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case err := <-res.Err:
			return last, err
		case m := <-res.Messages:
			if id, e, ok := exitFromEvent(m); ok {
				c.mu.Lock()
				c.exits[id] = e
				c.mu.Unlock()
				last = e.at
			}
		}
	}
}

// exitFromEvent saca el exit code y la hora de un evento "die".
func exitFromEvent(m events.Message) (string, exitInfo, bool) {
	if m.Type != events.ContainerEventType || m.Action != events.ActionDie || m.Actor.ID == "" {
		return "", exitInfo{}, false
	}
	code, err := strconv.Atoi(m.Actor.Attributes["exitCode"])
	if err != nil {
		return "", exitInfo{}, false
	}
	at := time.Unix(0, m.TimeNano)
	if m.TimeNano == 0 {
		at = time.Unix(m.Time, 0)
	}
	return m.Actor.ID, exitInfo{code: code, at: at}, true
}

// fromSummary pasa del tipo del SDK al que viaja al hub. st es el estado
// del inspect (puede ser nil): de ahí salen el exit code y la hora de
// arranque, que el listado no trae.
func fromSummary(s container.Summary, st *container.State, restarts int) agent.Container {
	var name string
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/") // la API los da como "/nombre"
	}
	var health string
	if s.Health != nil && s.Health.Status != container.NoHealthcheck {
		health = string(s.Health.Status)
	}
	c := agent.Container{
		ID:       s.ID,
		Name:     name,
		Image:    s.Image,
		State:    string(s.State),
		Health:   health,
		Restarts: restarts,
		Labels:   s.Labels,
	}
	if st != nil {
		c.ExitCode = st.ExitCode
		// Un contenedor que nunca arrancó tiene "0001-01-01T00:00:00Z", que
		// parsea al tiempo cero: queda igual que si no viniera.
		if t, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil && !t.IsZero() {
			c.StartedAt = t
		}
	}
	return c
}
