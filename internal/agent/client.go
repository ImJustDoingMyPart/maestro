package agent

import (
	"bytes"
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
	return c.call(ctx, http.MethodGet, path, nil, out)
}

// call hace un pedido corto con un cuerpo JSON opcional y decodifica la
// respuesta en out.
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	res, err := c.doMethod(ctx, c.http, method, path, body)
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
	return c.doMethod(ctx, hc, http.MethodGet, path, nil)
}

func (c *Client) doMethod(ctx context.Context, hc *http.Client, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	defer res.Body.Close()
	// El cuerpo de un error del agent es un ErrorBody; si del otro lado
	// contesta otra cosa, se muestra recortado.
	raw, _ := io.ReadAll(io.LimitReader(res.Body, maxFileSize))
	se := &statusError{code: res.StatusCode}
	if json.Unmarshal(raw, &se.body) != nil || se.body.Error == "" {
		se.body = ErrorBody{Error: strings.TrimSpace(string(raw[:min(len(raw), 512)]))}
	}
	se.msg = fmt.Sprintf("agent %s%s: %s: %s", c.baseURL, req.URL.Path, res.Status, se.body.Error)
	return nil, se
}

// statusError es una respuesta del agent distinta de 200.
type statusError struct {
	code int
	body ErrorBody
	msg  string
}

func (e *statusError) Error() string { return e.msg }

// StackFiles lee el compose y el .env de un proyecto.
func (c *Client) StackFiles(ctx context.Context, project string) (StackFiles, error) {
	var out StackFiles
	err := c.call(ctx, http.MethodGet, "/v1/stacks/"+url.PathEscape(project)+"/files", nil, &out)
	return out, stackErr(err)
}

// CheckStackFile valida un archivo sin escribirlo y devuelve el diff.
func (c *Client) CheckStackFile(ctx context.Context, project, name, content string) (CheckResult, error) {
	var out CheckResult
	path := "/v1/stacks/" + url.PathEscape(project) + "/files/" + url.PathEscape(name) + "/check"
	err := c.call(ctx, http.MethodPost, path, WriteRequest{Content: content}, &out)
	return out, stackErr(err)
}

// WriteStackFile valida y escribe un archivo. hash es el que se leyó.
func (c *Client) WriteStackFile(ctx context.Context, project, name, content, hash string) (WriteResult, error) {
	var out WriteResult
	path := "/v1/stacks/" + url.PathEscape(project) + "/files/" + url.PathEscape(name)
	err := c.call(ctx, http.MethodPut, path, WriteRequest{Content: content, Hash: hash}, &out)
	return out, stackErr(err)
}

// stackErr vuelve a convertir las respuestas de error del agent en los
// errores de StackSource, para que el hub los distinga con errors.Is y
// errors.As igual que si hablara con el Manager directo.
func stackErr(err error) error {
	se := (*statusError)(nil)
	if !errors.As(err, &se) {
		return err
	}
	switch se.code {
	case http.StatusUnprocessableEntity:
		return &InvalidError{Output: se.body.Output}
	case http.StatusNotFound:
		switch se.body.Error {
		case ErrBadFile.Error():
			return ErrBadFile
		case ErrBadAction.Error():
			return ErrBadAction
		}
		return fmt.Errorf("%w: %s", ErrStackNotFound, se.body.Error)
	case http.StatusConflict:
		if se.body.Error == ErrBusy.Error() {
			return ErrBusy
		}
		return ErrConflict
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrOutsideRoot, se.body.Error)
	}
	return err
}

// StackAction corre una acción de compose y devuelve su salida a medida que
// sale. El iterador termina con el evento de Exit; si el stream se corta
// antes, el último elemento es un error. Cortar el range cierra la conexión
// pero no el comando, que en el agent sigue hasta el final.
func (c *Client) StackAction(ctx context.Context, project, action string) (iter.Seq2[ActionEvent, error], error) {
	path := "/v1/stacks/" + url.PathEscape(project) + "/" + url.PathEscape(action)
	res, err := c.doMethod(ctx, c.stream, http.MethodPost, path, nil)
	if err != nil {
		return nil, stackErr(err)
	}
	return func(yield func(ActionEvent, error) bool) {
		defer res.Body.Close()
		dec := json.NewDecoder(res.Body)
		for {
			var ev ActionEvent
			err := dec.Decode(&ev)
			if errors.Is(err, io.EOF) {
				yield(ActionEvent{}, fmt.Errorf("agent %s: %s de %s: el stream terminó sin el código de salida", c.baseURL, action, project))
				return
			}
			if err != nil {
				yield(ActionEvent{}, fmt.Errorf("agent %s: %s de %s: %w", c.baseURL, action, project, err))
				return
			}
			if !yield(ev, nil) || ev.Exit != nil {
				return
			}
		}
	}, nil
}
