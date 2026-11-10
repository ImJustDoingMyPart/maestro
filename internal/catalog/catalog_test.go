package catalog

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// compose arma un contenedor de compose en el host "vm" con los labels de
// compose más los extra.
func compose(name, project, service string, extra map[string]string) Container {
	labels := map[string]string{
		"com.docker.compose.project":              project,
		"com.docker.compose.service":              service,
		"com.docker.compose.project.config_files": "/home/anon/docker/" + project + "/compose.yml",
	}
	for k, v := range extra {
		labels[k] = v
	}
	return Container{
		Host:   "vm",
		ID:     "id-" + name,
		Name:   name,
		Image:  "img/" + service + ":latest",
		State:  "running",
		Labels: labels,
	}
}

// composeService es el Service que sale de compose() sin otros labels.
func composeService(project, service string, containers ...Container) Service {
	return Service{
		ID:           project + "/" + service,
		Source:       SourceCompose,
		Name:         service,
		Group:        project,
		Host:         "vm",
		Project:      project,
		ComposeFiles: []string{"/home/anon/docker/" + project + "/compose.yml"},
		Containers:   containers,
	}
}

func ptr[T any](v T) *T { return &v }

func TestBuild(t *testing.T) {
	jellyfin := compose("jellyfin", "media-stack", "jellyfin", nil)

	homepageLabels := map[string]string{
		"homepage.name":        "Jellyfin HP",
		"homepage.group":       "Media HP",
		"homepage.icon":        "jellyfin.png",
		"homepage.href":        "https://hp.example.org",
		"homepage.description": "desc HP",
	}
	maestroLabels := map[string]string{
		"maestro.name":        "Jellyfin M",
		"maestro.group":       "Media M",
		"maestro.icon":        "jellyfin",
		"maestro.url":         "https://m.example.org",
		"maestro.description": "desc M",
	}
	both := map[string]string{}
	for k, v := range homepageLabels {
		both[k] = v
	}
	for k, v := range maestroLabels {
		both[k] = v
	}
	withCheck := map[string]string{
		"maestro.check.http":        "http://jellyfin:8096/health",
		"maestro.check.http.expect": "200",
	}
	labelCheck := []Check{{Kind: CheckHTTP, Target: "http://jellyfin:8096/health", Expect: 200}}
	fileCheck := []Check{{Kind: CheckHTTP, Target: "http://otro/", Expect: 204}}

	merge := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}

	tests := []struct {
		name       string
		containers []Container
		file       map[string]FileService
		want       []Service
	}{
		{
			name:       "nada",
			containers: nil,
			file:       nil,
			want:       []Service{},
		},
		{
			name:       "solo labels de compose",
			containers: []Container{jellyfin},
			want:       []Service{composeService("media-stack", "jellyfin", jellyfin)},
		},
		{
			name:       "homepage completa nombre, grupo, ícono, URL y descripción",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", homepageLabels)},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", homepageLabels))
				s.Name, s.Group, s.Icon, s.URL, s.Description = "Jellyfin HP", "Media HP", "jellyfin.png", "https://hp.example.org", "desc HP"
				return s
			}()},
		},
		{
			name:       "maestro pisa a homepage",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", both)},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", both))
				s.Name, s.Group, s.Icon, s.URL, s.Description = "Jellyfin M", "Media M", "jellyfin", "https://m.example.org", "desc M"
				return s
			}()},
		},
		{
			name:       "el archivo pisa a maestro, campo por campo",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", both)},
			file: map[string]FileService{
				"media-stack/jellyfin": {Name: "Jellyfin F", URL: "https://f.example.org"},
			},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", both))
				s.Name, s.Group, s.Icon, s.URL, s.Description = "Jellyfin F", "Media M", "jellyfin", "https://f.example.org", "desc M"
				s.NameFromFile = true
				return s
			}()},
		},
		{
			name: "entrada con / sin contenedor no crea nada",
			file: map[string]FileService{
				"media-stack/jellyfin": {Name: "Jellyfin F", Unit: "x.service"},
			},
			want: []Service{},
		},
		{
			name:       "maestro.ignore lo saca",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", map[string]string{"maestro.ignore": "true"})},
			want:       []Service{},
		},
		{
			name:       "ignore del archivo lo saca",
			containers: []Container{jellyfin},
			file:       map[string]FileService{"media-stack/jellyfin": {Ignore: ptr(true)}},
			want:       []Service{},
		},
		{
			name:       "ignore false del archivo pisa al label",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", map[string]string{"maestro.ignore": "true"})},
			file:       map[string]FileService{"media-stack/jellyfin": {Ignore: ptr(false)}},
			want: []Service{composeService("media-stack", "jellyfin",
				compose("jellyfin", "media-stack", "jellyfin", map[string]string{"maestro.ignore": "true"}))},
		},
		{
			name: "réplicas: un servicio, contenedores ordenados, labels de la primera",
			containers: []Container{
				compose("app-2", "web", "app", map[string]string{"maestro.name": "Dos"}),
				compose("app-1", "web", "app", map[string]string{"maestro.name": "Uno"}),
			},
			want: []Service{func() Service {
				s := composeService("web", "app",
					compose("app-1", "web", "app", map[string]string{"maestro.name": "Uno"}),
					compose("app-2", "web", "app", map[string]string{"maestro.name": "Dos"}),
				)
				s.Name = "Uno"
				return s
			}()},
		},
		{
			name: "mismo nombre de servicio en dos proyectos son dos servicios",
			containers: []Container{
				compose("immich_postgres", "immich", "database", nil),
				compose("otro_db", "otro", "database", nil),
			},
			want: []Service{
				composeService("immich", "database", compose("immich_postgres", "immich", "database", nil)),
				composeService("otro", "database", compose("otro_db", "otro", "database", nil)),
			},
		},
		{
			name:       "contenedor suelto, sin compose ni labels",
			containers: []Container{{Host: "vm", ID: "id-x", Name: "suelto", State: "running"}},
			want: []Service{{
				ID: "suelto", Source: SourceContainer, Name: "suelto", Host: "vm",
				Containers: []Container{{Host: "vm", ID: "id-x", Name: "suelto", State: "running"}},
			}},
		},
		{
			name:       "contenedor suelto con labels maestro",
			containers: []Container{{Host: "vm", Name: "suelto", Labels: map[string]string{"maestro.group": "Varios"}}},
			want: []Service{{
				ID: "suelto", Source: SourceContainer, Name: "suelto", Group: "Varios", Host: "vm",
				Containers: []Container{{Host: "vm", Name: "suelto", Labels: map[string]string{"maestro.group": "Varios"}}},
			}},
		},
		{
			name:       "clave sin / no pisa a un contenedor suelto con el mismo nombre",
			containers: []Container{{Host: "vm", Name: "router"}},
			file:       map[string]FileService{"router": {Name: "Router", Group: "Red"}},
			want: []Service{
				{ID: "router", Source: SourceContainer, Name: "router", Host: "vm", Containers: []Container{{Host: "vm", Name: "router"}}},
				{ID: "router", Source: SourceExternal, Name: "Router", Group: "Red", NameFromFile: true},
			},
		},
		{
			name: "varios archivos de compose",
			containers: []Container{compose("a", "p", "a", map[string]string{
				"com.docker.compose.project.config_files": "/s/p/compose.yml,/s/p/compose.override.yml",
			})},
			want: []Service{func() Service {
				s := composeService("p", "a", compose("a", "p", "a", map[string]string{
					"com.docker.compose.project.config_files": "/s/p/compose.yml,/s/p/compose.override.yml",
				}))
				s.ComposeFiles = []string{"/s/p/compose.yml", "/s/p/compose.override.yml"}
				return s
			}()},
		},
		{
			name: "servicio systemd del archivo",
			file: map[string]FileService{
				"adguard": {
					Name: "AdGuard Home", Group: "Red", Host: "adguard", Unit: "AdGuardHome.service",
					URL: "https://adguard.example.org", Checks: []Check{{Kind: CheckHTTP, Target: "http://10.0.0.2/"}},
				},
			},
			want: []Service{{
				ID: "adguard", Source: SourceSystemd, Name: "AdGuard Home", NameFromFile: true, Group: "Red", Host: "adguard",
				Unit: "AdGuardHome.service", URL: "https://adguard.example.org",
				Checks: []Check{{Kind: CheckHTTP, Target: "http://10.0.0.2/"}}, Alert: true,
			}},
		},
		{
			name: "servicio externo del archivo, sin nombre usa la clave",
			file: map[string]FileService{"router": {Group: "Red"}},
			want: []Service{{ID: "router", Source: SourceExternal, Name: "router", Group: "Red"}},
		},
		{
			name:       "check de labels activa la alerta",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", withCheck)},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", withCheck))
				s.Checks, s.Alert = labelCheck, true
				return s
			}()},
		},
		{
			name:       "check sin expect",
			containers: []Container{compose("j", "p", "j", map[string]string{"maestro.check.http": "http://j/"})},
			want: []Service{func() Service {
				s := composeService("p", "j", compose("j", "p", "j", map[string]string{"maestro.check.http": "http://j/"}))
				s.Checks, s.Alert = []Check{{Kind: CheckHTTP, Target: "http://j/"}}, true
				return s
			}()},
		},
		{
			name:       "maestro.alert false con check",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", merge(withCheck, map[string]string{"maestro.alert": "false"}))},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin",
					compose("jellyfin", "media-stack", "jellyfin", merge(withCheck, map[string]string{"maestro.alert": "false"})))
				s.Checks, s.Alert = labelCheck, false
				return s
			}()},
		},
		{
			name:       "maestro.alert true sin checks",
			containers: []Container{compose("j", "p", "j", map[string]string{"maestro.alert": "true"})},
			want: []Service{func() Service {
				s := composeService("p", "j", compose("j", "p", "j", map[string]string{"maestro.alert": "true"}))
				s.Alert = true
				return s
			}()},
		},
		{
			name:       "alert false del archivo pisa al label true",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", merge(withCheck, map[string]string{"maestro.alert": "true"}))},
			file:       map[string]FileService{"media-stack/jellyfin": {Alert: ptr(false)}},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin",
					compose("jellyfin", "media-stack", "jellyfin", merge(withCheck, map[string]string{"maestro.alert": "true"})))
				s.Checks, s.Alert = labelCheck, false
				return s
			}()},
		},
		{
			name:       "checks del archivo reemplazan a los de labels",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", withCheck)},
			file:       map[string]FileService{"media-stack/jellyfin": {Checks: fileCheck}},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", withCheck))
				s.Checks, s.Alert = fileCheck, true
				return s
			}()},
		},
		{
			name:       "checks vacíos (no nil) del archivo los borran",
			containers: []Container{compose("jellyfin", "media-stack", "jellyfin", withCheck)},
			file:       map[string]FileService{"media-stack/jellyfin": {Checks: []Check{}}},
			want: []Service{func() Service {
				s := composeService("media-stack", "jellyfin", compose("jellyfin", "media-stack", "jellyfin", withCheck))
				s.Alert = false
				return s
			}()},
		},
		{
			name: "valores inválidos se ignoran",
			containers: []Container{compose("j", "p", "j", map[string]string{
				"maestro.check.http":        "http://j/",
				"maestro.check.http.expect": "abc",
				"maestro.alert":             "quizás",
				"maestro.ignore":            "tal vez",
			})},
			want: []Service{func() Service {
				s := composeService("p", "j", compose("j", "p", "j", map[string]string{
					"maestro.check.http":        "http://j/",
					"maestro.check.http.expect": "abc",
					"maestro.alert":             "quizás",
					"maestro.ignore":            "tal vez",
				}))
				s.Checks, s.Alert = []Check{{Kind: CheckHTTP, Target: "http://j/"}}, true
				return s
			}()},
		},
		{
			name: "check TCP, intervalo, timeout y sin redirecciones",
			containers: []Container{compose("j", "p", "j", map[string]string{
				"maestro.check.http":           "http://j/",
				"maestro.check.http.redirects": "false",
				"maestro.check.tcp":            "j:5432",
				"maestro.check.interval":       "30s",
				"maestro.check.timeout":        "5s",
			})},
			want: []Service{func() Service {
				s := composeService("p", "j", compose("j", "p", "j", map[string]string{
					"maestro.check.http":           "http://j/",
					"maestro.check.http.redirects": "false",
					"maestro.check.tcp":            "j:5432",
					"maestro.check.interval":       "30s",
					"maestro.check.timeout":        "5s",
				}))
				s.Checks = []Check{
					{Kind: CheckHTTP, Target: "http://j/", NoRedirects: true, Interval: 30 * time.Second, Timeout: 5 * time.Second},
					{Kind: CheckTCP, Target: "j:5432", Interval: 30 * time.Second, Timeout: 5 * time.Second},
				}
				s.Alert = true
				return s
			}()},
		},
		{
			name: "TCP sin puerto, intervalo inválido y expect fuera de rango se ignoran",
			containers: []Container{compose("j", "p", "j", map[string]string{
				"maestro.check.http":        "http://j/",
				"maestro.check.http.expect": "42",
				"maestro.check.tcp":         "j",
				"maestro.check.interval":    "un rato",
				"maestro.check.timeout":     "-5s",
			})},
			want: []Service{func() Service {
				s := composeService("p", "j", compose("j", "p", "j", map[string]string{
					"maestro.check.http":        "http://j/",
					"maestro.check.http.expect": "42",
					"maestro.check.tcp":         "j",
					"maestro.check.interval":    "un rato",
					"maestro.check.timeout":     "-5s",
				}))
				s.Checks, s.Alert = []Check{{Kind: CheckHTTP, Target: "http://j/"}}, true
				return s
			}()},
		},
		{
			name:       "maestro.separate y el archivo que lo pisa",
			containers: []Container{compose("a", "p", "a", map[string]string{"maestro.separate": "true"}), compose("b", "p", "b", map[string]string{"maestro.separate": "true"})},
			file:       map[string]FileService{"p/b": {Separate: ptr(false)}},
			want: []Service{
				func() Service {
					s := composeService("p", "a", compose("a", "p", "a", map[string]string{"maestro.separate": "true"}))
					s.Separate = true
					return s
				}(),
				composeService("p", "b", compose("b", "p", "b", map[string]string{"maestro.separate": "true"})),
			},
		},
		{
			name: "orden por grupo, nombre e ID",
			containers: []Container{
				compose("z", "b-proj", "z", nil),
				compose("a", "b-proj", "a", nil),
				compose("m", "a-proj", "m", nil),
				compose("x1", "c-proj", "x", map[string]string{"maestro.group": "b-proj", "maestro.name": "a"}),
			},
			file: map[string]FileService{"ext": {Group: "a-proj", Name: "zz"}},
			want: []Service{
				composeService("a-proj", "m", compose("m", "a-proj", "m", nil)),
				{ID: "ext", Source: SourceExternal, Name: "zz", Group: "a-proj", NameFromFile: true},
				composeService("b-proj", "a", compose("a", "b-proj", "a", nil)),
				func() Service {
					s := composeService("c-proj", "x", compose("x1", "c-proj", "x", map[string]string{"maestro.group": "b-proj", "maestro.name": "a"}))
					s.Name, s.Group = "a", "b-proj"
					return s
				}(),
				composeService("b-proj", "z", compose("z", "b-proj", "z", nil)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(slog.New(slog.DiscardHandler))
			got := c.Build(tt.containers, tt.file)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Build() distinto\n got: %s\nwant: %s", dump(got), dump(tt.want))
			}
		})
	}
}

// dump imprime un servicio por línea para que el diff de un fallo se lea.
func dump(ss []Service) string {
	var b strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&b, "\n  %+v", s)
	}
	return b.String()
}

func TestBuildNoModificaLaEntrada(t *testing.T) {
	containers := []Container{
		compose("app-2", "web", "app", nil),
		compose("app-1", "web", "app", nil),
	}
	New(slog.New(slog.DiscardHandler)).Build(containers, nil)
	if containers[0].Name != "app-2" || containers[1].Name != "app-1" {
		t.Errorf("Build reordenó el slice de entrada: %s, %s", containers[0].Name, containers[1].Name)
	}
}

func TestAvisosUnaVez(t *testing.T) {
	var buf bytes.Buffer
	c := New(slog.New(slog.NewTextHandler(&buf, nil)))

	containers := []Container{
		compose("a", "p", "a", map[string]string{
			"maestro.colour":      "rojo", // clave desconocida
			"maestro.alert":       "quizás",
			"homepage.widget.url": "http://x", // homepage desconocida: sin aviso
			"maestro.check.push":  "secreto",  // va en el archivo
		}),
		compose("b", "p", "b", map[string]string{"maestro.colour": "azul"}),
	}
	c.Build(containers, nil)
	c.Build(containers, nil)

	out := buf.String()
	if n := strings.Count(out, "maestro.colour"); n != 1 {
		t.Errorf("maestro.colour aparece %d veces en el log, quería 1:\n%s", n, out)
	}
	if n := strings.Count(out, "maestro.alert"); n != 1 {
		t.Errorf("maestro.alert (valor inválido) aparece %d veces en el log, quería 1:\n%s", n, out)
	}
	if !strings.Contains(out, "va en maestro.yaml") {
		t.Errorf("maestro.check.push tiene que avisar que va en el archivo:\n%s", out)
	}
	if strings.Contains(out, "homepage.") {
		t.Errorf("no debería avisar por claves homepage.*:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("los avisos tienen que ser Warn:\n%s", out)
	}
	for _, known := range []string{"maestro.name", "com.docker"} {
		if strings.Contains(out, known) {
			t.Errorf("avisó por una clave conocida (%s):\n%s", known, out)
		}
	}
}

func TestBuildConcurrente(t *testing.T) {
	c := New(slog.New(slog.DiscardHandler))
	containers := []Container{compose("a", "p", "a", map[string]string{"maestro.desconocida": "x"})}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got := c.Build(containers, nil); len(got) != 1 {
				t.Errorf("Build devolvió %d servicios, quería 1", len(got))
			}
		})
	}
	wg.Wait()
}
