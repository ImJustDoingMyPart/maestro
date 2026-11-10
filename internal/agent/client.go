package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client habla con un agent. Lo usa el hub.
type Client struct {
	baseURL string
	token   string
	http    *http.Client // pedidos cortos, con timeout
	stream  *http.Client // streams: sin timeout, los corta el contexto
}

// NewClient crea un cliente para el agent en baseURL (por ejemplo
// "http://192.168.0.11:9101").
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
		// http.Client.Timeout cuenta hasta terminar de leer el cuerpo, así
		// que mataría un stream de logs a los 20 s.
		stream: &http.Client{},
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

// Logs abre los logs de un contenedor del host. Tiene el mismo contrato que
// ContainerSource.Logs: el error de apertura sale enseguida
// (ErrContainerNotFound si el agent contesta 404) y el iterador hay que
// recorrerlo para que se cierre la conexión.
func (c *Client) Logs(ctx context.Context, id string, opts LogOptions) (iter.Seq2[LogLine, error], error) {
	path := "/v1/containers/" + url.PathEscape(id) + "/logs?" + opts.encode().Encode()
	res, err := c.do(ctx, c.stream, path)
	if se := (*statusError)(nil); errors.As(err, &se) && se.code == http.StatusNotFound {
		return nil, ErrContainerNotFound
	}
	if err != nil {
		return nil, err
	}

	return func(yield func(LogLine, error) bool) {
		defer res.Body.Close()
		dec := json.NewDecoder(res.Body)
		for {
			var l LogLine
			err := dec.Decode(&l)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(LogLine{}, fmt.Errorf("agent %s: logs de %s: %w", c.baseURL, id, err))
				return
			}
			if !yield(l, nil) {
				return
			}
		}
	}, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	res, err := c.do(ctx, c.http, path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("agent %s%s: respuesta inválida: %w", c.baseURL, path, err)
	}
	return nil
}

// do hace un GET con el token. Si la respuesta no es 200 la cierra y
// devuelve el error; si es 200, cerrar el cuerpo queda para quien llama.
func (c *Client) do(ctx context.Context, hc *http.Client, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	defer res.Body.Close()
	// El cuerpo de un error del agent es {"error": "..."}; se muestra
	// recortado por si del otro lado contesta otra cosa.
	body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return nil, &statusError{
		code: res.StatusCode,
		msg:  fmt.Sprintf("agent %s%s: %s: %s", c.baseURL, req.URL.Path, res.Status, strings.TrimSpace(string(body))),
	}
}

// statusError es una respuesta del agent distinta de 200.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }
