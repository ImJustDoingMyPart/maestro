package stacks

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestList(t *testing.T) {
	needTools(t)
	m, _ := setup(t)
	root := m.root
	// Con name: en el archivo, el proyecto es ese y no el de la carpeta.
	must(t, os.Mkdir(filepath.Join(root, "Media Stack"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "Media Stack", "docker-compose.yml"), []byte("services:\n  b: {image: alpine, labels: {homepage.group: Media}}\n  a: {image: alpine}\n"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, "nombrado"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "nombrado", "compose.yaml"), []byte("name: otro\nservices:\n  x: {image: alpine}\n"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, "roto"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "roto", "compose.yml"), []byte("services: [\n"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, "sin-compose"), 0o755))
	must(t, os.Mkdir(filepath.Join(root, ".oculta"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".oculta", "compose.yml"), []byte("services:\n  x: {image: alpine}\n"), 0o644))

	list, err := m.List(context.Background())
	must(t, err)
	got := map[string][]string{}
	errs := map[string]string{}
	for _, s := range list {
		for _, svc := range s.Services {
			got[s.Project] = append(got[s.Project], svc.Name)
		}
		errs[s.Project] = s.Err
	}
	if len(list) != 4 {
		t.Fatalf("List = %+v, quería demo, mediastack, otro y roto", list)
	}
	if !slices.Equal(got["demo"], []string{"web"}) || !slices.Equal(got["mediastack"], []string{"a", "b"}) || !slices.Equal(got["otro"], []string{"x"}) {
		t.Errorf("servicios = %v", got)
	}
	for _, s := range list {
		if s.Project == "mediastack" && s.Services[1].Labels["homepage.group"] != "Media" {
			t.Errorf("labels de b = %v", s.Services[1].Labels)
		}
	}
	if errs["roto"] == "" || len(got["roto"]) != 0 {
		t.Errorf("roto tendría que venir con error y sin servicios: %q %v", errs["roto"], got["roto"])
	}

	// Sin cambios en los archivos no vuelve a correr compose: se nota
	// rompiendo el binario.
	m.docker = "/no/existe"
	again, err := m.List(context.Background())
	must(t, err)
	if len(again) != 4 || again[0].Err != "" {
		t.Errorf("con caché = %+v", again)
	}
	must(t, os.WriteFile(filepath.Join(root, "demo", "compose.yml"), []byte(goodCompose+"\n"), 0o640))
	again, _ = m.List(context.Background())
	for _, s := range again {
		if s.Project == "demo" && s.Err == "" {
			t.Error("con el archivo cambiado tendría que volver a correr compose (y fallar)")
		}
	}
}

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{"Media Stack": "mediastack", "ia-stack": "ia-stack", "jdownloader-2": "jdownloader-2", "_x.y": "xy"} {
		if got := projectName(in); got != want {
			t.Errorf("projectName(%q) = %q, quería %q", in, got, want)
		}
	}
}
