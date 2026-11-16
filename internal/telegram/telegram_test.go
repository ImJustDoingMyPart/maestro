package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "123456:secreto-del-bot_abcdefghijklmn"

// fakeAPI responde con reply a cada sendMessage y guarda lo que recibió.
func fakeAPI(t *testing.T, reply func(req sendRequest) (int, string)) (*httptest.Server, *[]sendRequest) {
	t.Helper()
	var got []sendRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendMessage" || r.Method != http.MethodPost {
			t.Errorf("pedido a %s %s", r.Method, r.URL.Path)
		}
		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("cuerpo: %v", err)
		}
		got = append(got, req)
		code, body := reply(req)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSend(t *testing.T) {
	srv, got := fakeAPI(t, func(sendRequest) (int, string) { return 200, `{"ok":true,"result":{}}` })
	if err := New(token, srv.URL).Send(context.Background(), "-100123", "<b>hola</b>"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("%d pedidos, quiero 1", len(*got))
	}
	r := (*got)[0]
	if r.ChatID != "-100123" || r.Text != "<b>hola</b>" || r.ParseMode != "HTML" || !r.LinkPreview.Disabled {
		t.Errorf("pedido: %+v", r)
	}
}

// Si Telegram no puede leer el HTML, va el mismo texto sin formato.
func TestSendSinFormatoSiFallaElHTML(t *testing.T) {
	srv, got := fakeAPI(t, func(req sendRequest) (int, string) {
		if req.ParseMode == "HTML" {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unsupported start tag \"x\""}`
		}
		return 200, `{"ok":true}`
	})
	if err := New(token, srv.URL).Send(context.Background(), "1", "<b>a &lt; b</b> <x>"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("%d pedidos, quiero 2", len(*got))
	}
	if r := (*got)[1]; r.ParseMode != "" || r.Text != "a < b " {
		t.Errorf("segundo pedido: %+v", r)
	}
}

func TestSendErrores(t *testing.T) {
	tests := []struct {
		name      string
		code      int
		body      string
		retry     bool
		wait      time.Duration
		mentioned string
	}{
		{"chat que no existe", 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, false, 0, "chat not found"},
		{"token revocado", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, false, 0, "401"},
		{"demasiados mensajes", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`, true, 7 * time.Second, "429"},
		{"Telegram caído", 502, `<html>Bad Gateway</html>`, true, 0, "502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, func(sendRequest) (int, string) { return tt.code, tt.body })
			err := New(token, srv.URL).Send(context.Background(), "1", "x")
			if err == nil || !strings.Contains(err.Error(), tt.mentioned) {
				t.Fatalf("error %v, quiero que mencione %q", err, tt.mentioned)
			}
			if retry, wait := Retryable(err); retry != tt.retry || wait != tt.wait {
				t.Errorf("Retryable = %v, %v; quiero %v, %v", retry, wait, tt.retry, tt.wait)
			}
		})
	}
}

// Un error de red de net/http trae la URL, y la URL trae el token.
func TestErrorDeRedSinToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // nadie escucha: connection refused
	err := New(token, srv.URL).Send(context.Background(), "1", "x")
	if err == nil {
		t.Fatal("sin error")
	}
	if strings.Contains(err.Error(), "secreto") {
		t.Errorf("el token está en el error: %v", err)
	}
	if retry, _ := Retryable(err); !retry {
		t.Errorf("un error de red se reintenta: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = New(token, srv.URL).Send(ctx, "1", "x")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secreto") {
		t.Errorf("con el contexto cancelado: %v", err)
	}
	if retry, _ := Retryable(err); retry {
		t.Error("un contexto cancelado no se reintenta")
	}
}

func TestEscapeYStrip(t *testing.T) {
	in := `check http: <timeout> & "algo"`
	esc := Escape(in)
	if strings.ContainsAny(esc, "<>") {
		t.Errorf("Escape(%q) = %q", in, esc)
	}
	if got := Strip("<b>" + esc + "</b>"); got != in {
		t.Errorf("Strip = %q, quiero %q", got, in)
	}
}
