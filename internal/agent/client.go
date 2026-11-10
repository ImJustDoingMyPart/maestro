package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client habla con un agent. Lo usa el hub.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient crea un cliente para el agent en baseURL (por ejemplo
// "http://192.168.0.11:9101").
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Containers pide la lista de contenedores del host.
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var resp ContainersResponse
	if err := c.get(ctx, "/v1/containers", &resp); err != nil {
		return nil, err
	}
	return resp.Containers, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		// El cuerpo de un error del agent es {"error": "..."}; se muestra
		// recortado por si del otro lado contesta otra cosa.
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("agent %s%s: %s: %s", c.baseURL, path, res.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("agent %s%s: respuesta inválida: %w", c.baseURL, path, err)
	}
	return nil
}
