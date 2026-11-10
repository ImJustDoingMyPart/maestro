package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeStacks guarda lo último que recibió y devuelve err si no es nil.
type fakeStacks struct {
	err     error
	project string
	name    string
	content string
	hash    string
}

func (f *fakeStacks) Files(_ context.Context, project string) (StackFiles, error) {
	f.project = project
	if f.err != nil {
		return StackFiles{}, f.err
	}
	return StackFiles{Project: project, Files: []StackFile{{Name: FileCompose, Content: "services: {}\n", Hash: "sha256:1", Exists: true}}}, nil
}

func (f *fakeStacks) Check(_ context.Context, project, name string, content []byte) (CheckResult, error) {
	f.project, f.name, f.content = project, name, string(content)
	return CheckResult{Diff: "+x"}, f.err
}

func (f *fakeStacks) Write(_ context.Context, project, name string, content []byte, hash string) (WriteResult, error) {
	f.project, f.name, f.content, f.hash = project, name, string(content), hash
	return WriteResult{Hash: "sha256:2", Diff: "+x"}, f.err
}

func newStackClient(t *testing.T, st StackSource) *Client {
	t.Helper()
	h, err := NewHandler(fakeSource{}, st, testToken, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken)
}

func TestStacksIdaYVuelta(t *testing.T) {
	st := &fakeStacks{}
	c := newStackClient(t, st)
	ctx := context.Background()

	files, err := c.StackFiles(ctx, "media-stack")
	if err != nil || st.project != "media-stack" || files.Files[0].Hash != "sha256:1" {
		t.Fatalf("StackFiles = %+v, %v (recibió %q)", files, err, st.project)
	}
	if _, err := c.CheckStackFile(ctx, "demo", FileEnv, "A=1\n"); err != nil || st.name != FileEnv || st.content != "A=1\n" {
		t.Fatalf("CheckStackFile: %v, recibió %+v", err, st)
	}
	res, err := c.WriteStackFile(ctx, "demo", FileCompose, "services: {}\n", "sha256:1")
	if err != nil || res.Hash != "sha256:2" || st.hash != "sha256:1" {
		t.Fatalf("WriteStackFile = %+v, %v, recibió %+v", res, err, st)
	}
}

// Los errores del Manager tienen que llegar al hub como los mismos errores.
func TestStacksErrores(t *testing.T) {
	tests := []struct {
		err  error
		want error
	}{
		{ErrConflict, ErrConflict},
		{ErrBusy, ErrBusy},
		{ErrBadFile, ErrBadFile},
		{fmt.Errorf("%w: /x no existe", ErrStackNotFound), ErrStackNotFound},
		{fmt.Errorf("%w: /etc", ErrOutsideRoot), ErrOutsideRoot},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			c := newStackClient(t, &fakeStacks{err: tt.err})
			_, err := c.WriteStackFile(context.Background(), "demo", FileCompose, "x", "")
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, quería %v", err, tt.want)
			}
		})
	}

	t.Run("inválido", func(t *testing.T) {
		c := newStackClient(t, &fakeStacks{err: &InvalidError{Output: "línea 3: mapping values"}})
		_, err := c.CheckStackFile(context.Background(), "demo", FileCompose, "x")
		var inv *InvalidError
		if !errors.As(err, &inv) || inv.Output != "línea 3: mapping values" {
			t.Errorf("err = %v, quería InvalidError con la salida", err)
		}
	})

	t.Run("error interno no filtra detalles", func(t *testing.T) {
		c := newStackClient(t, &fakeStacks{err: errors.New("open /home/anon/secreto: permission denied")})
		_, err := c.StackFiles(context.Background(), "demo")
		if err == nil || strings.Contains(err.Error(), "secreto") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStacksPedidosInvalidos(t *testing.T) {
	h, _ := NewHandler(fakeSource{}, &fakeStacks{}, testToken, slog.New(slog.DiscardHandler))
	tests := []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/v1/stacks/Mayus/files", "", http.StatusBadRequest},
		{"GET", "/v1/stacks/..%2Fetc/files", "", http.StatusBadRequest},
		{"PUT", "/v1/stacks/demo/files/compose", "no es json", http.StatusBadRequest},
		{"PUT", "/v1/stacks/demo/files/compose", `{"content":"` + strings.Repeat("x", maxFileSize+1) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s %s = %d, quería %d (%s)", tt.method, tt.path, rec.Code, tt.want, rec.Body)
		}
	}

	// Sin --stacks-dir, 501.
	h, _ = NewHandler(fakeSource{}, nil, testToken, slog.New(slog.DiscardHandler))
	req := httptest.NewRequest("GET", "/v1/stacks/demo/files", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("sin stacks = %d, quería 501", rec.Code)
	}
}
