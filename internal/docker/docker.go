package docker

import (
	"context"
	"fmt"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// Client lee Docker. En esta etapa solo lista e inspecciona.
type Client struct {
	api *client.Client
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
	return &Client{api: api}, nil
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
		out = append(out, fromSummary(s, ins.Container.RestartCount))
	}
	return out, nil
}

// fromSummary pasa del tipo del SDK al que viaja al hub.
func fromSummary(s container.Summary, restarts int) agent.Container {
	var name string
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/") // la API los da como "/nombre"
	}
	var health string
	if s.Health != nil && s.Health.Status != container.NoHealthcheck {
		health = string(s.Health.Status)
	}
	return agent.Container{
		ID:       s.ID,
		Name:     name,
		Image:    s.Image,
		State:    string(s.State),
		Health:   health,
		Restarts: restarts,
		Labels:   s.Labels,
	}
}
