// Package telegram manda mensajes con la Bot API de Telegram
// (https://core.telegram.org/bots/api#sendmessage). Hace lo mismo que
// ~/docker/scripts/notify.sh en el server: HTML como formato y, si Telegram
// rechaza el HTML, el mismo texto sin formato antes de darse por vencido.
//
// El token va en la ruta de la URL ("/bot<token>/sendMessage"), así que un
// error de red de net/http lo llevaría adentro: los errores de este paquete
// nunca incluyen la URL.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBase es la dirección de la Bot API.
const DefaultBase = "https://api.telegram.org"

// Client manda mensajes con un bot. Se puede usar desde varias goroutines.
type Client struct {
	token string
	base  string
	http  *http.Client
}

// New arma un cliente para el bot de token. base vacío es DefaultBase (los
// tests pasan la de un httptest.Server).
func New(token, base string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{token: token, base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 20 * time.Second}}
}

// String no muestra el token.
func (c *Client) String() string { return "telegram.Client{token: [censurado]}" }

// Error es un rechazo de Telegram: ok false en la respuesta.
type Error struct {
	Code        int    // error_code: 400, 401, 403, 429…
	Description string // "Bad Request: chat not found"
	// RetryAfter es lo que pide esperar Telegram antes de reintentar (429).
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// Retryable dice si vale la pena reintentar después de err y cuánto pide
// esperar Telegram (cero si no dice). Se reintenta lo que puede pasar solo:
// la red, un 429 o un 5xx. Un token o un chat mal puestos (401, 403, 400)
// no se arreglan esperando.
func Retryable(err error) (bool, time.Duration) {
	var te *Error
	if errors.As(err, &te) {
		return te.Code == http.StatusTooManyRequests || te.Code >= 500, te.RetryAfter
	}
	return err != nil && !errors.Is(err, context.Canceled), 0
}

// Send manda text (HTML de Telegram) a chat. Si Telegram rechaza el HTML
// (una etiqueta mal cerrada, algo sin escapar), manda el mismo texto sin
// etiquetas: un aviso con formato roto es mejor que ningún aviso.
func (c *Client) Send(ctx context.Context, chat, text string) error {
	err := c.send(ctx, chat, text, "HTML")
	var te *Error
	if errors.As(err, &te) && te.Code == http.StatusBadRequest && strings.Contains(te.Description, "can't parse entities") {
		return c.send(ctx, chat, Strip(text), "")
	}
	return err
}

type sendRequest struct {
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	LinkPreview struct {
		Disabled bool `json:"is_disabled"`
	} `json:"link_preview_options"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) send(ctx context.Context, chat, text, mode string) error {
	body := sendRequest{ChatID: chat, Text: text, ParseMode: mode}
	body.LinkPreview.Disabled = true // el link a Maestro no necesita vista previa
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/sendMessage", bytes.NewReader(data))
	if err != nil {
		return errors.New("telegram: no se pudo armar el pedido") // el error trae la URL
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return redact(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return redact(err)
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return &Error{Code: resp.StatusCode, Description: "respuesta que no es JSON"}
	}
	if !r.OK {
		return &Error{
			Code: orInt(r.ErrorCode, resp.StatusCode), Description: r.Description,
			RetryAfter: time.Duration(r.Parameters.RetryAfter) * time.Second,
		}
	}
	return nil
}

// redact saca la URL (con el token) de un error de net/http y deja solo la
// causa: "dial tcp: lookup api.telegram.org: no such host". Mantiene la
// cadena de errores para errors.Is (context.Canceled, DeadlineExceeded).
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return &redacted{msg: tokenInURL.ReplaceAllString(err.Error(), "/bot[censurado]/"), err: err}
}

type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return "telegram: " + r.msg }
func (r *redacted) Unwrap() error { return r.err }

// tokenInURL es por si un error de más abajo también trae la URL.
var tokenInURL = regexp.MustCompile(`/bot[^/\s]+/`)

func orInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// Escape escapa texto de afuera (un nombre, un motivo) para meterlo en un
// mensaje HTML: Telegram rechaza el mensaje entero por un "<" suelto.
func Escape(s string) string { return html.EscapeString(s) }

var tag = regexp.MustCompile(`<[^>]*>`)

// Strip pasa un mensaje HTML a texto plano: sin etiquetas y con las
// entidades resueltas.
func Strip(s string) string { return html.UnescapeString(tag.ReplaceAllString(s, "")) }
