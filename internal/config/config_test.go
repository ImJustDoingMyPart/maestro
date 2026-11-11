package config

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// env arma una LookupFunc a partir de un mapa.
func env(vars map[string]string) LookupFunc {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func ptr[T any](v T) *T { return &v }

func TestParseValido(t *testing.T) {
	data := `
hosts:
  vm: { agent: "http://${VM_IP}:9101", token: "${TOKEN_VM}" }
services:
  adguard:
    name: AdGuard Home
    group: Red
    host: vm
    unit: AdGuardHome.service
    check: { http: http://10.0.0.2/, expect: 200 }
  media-stack/jellyfin:
    alert: false
  precio:
    description: "cuesta $5 y ${ esto no es variable"
`
	cfg, err := Parse([]byte(data), env(map[string]string{
		"VM_IP":    "192.168.0.11",
		"TOKEN_VM": "s3cr3t: con dos puntos # y numeral",
	}))
	if err != nil {
		t.Fatal(err)
	}

	want := &Config{
		Hosts: map[string]Host{
			"vm": {Agent: "http://192.168.0.11:9101", Token: "s3cr3t: con dos puntos # y numeral"},
		},
		Services: map[string]Service{
			"adguard": {
				Name: "AdGuard Home", Group: "Red", Host: "vm", Unit: "AdGuardHome.service",
				Check: &Check{HTTP: "http://10.0.0.2/", Expect: 200},
			},
			"media-stack/jellyfin": {Alert: ptr(false)},
			"precio":               {Description: "cuesta $5 y ${ esto no es variable"},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Parse:\n got %#v\nwant %#v", cfg, want)
	}
}

func TestParseVacio(t *testing.T) {
	for _, data := range []string{"", "# solo un comentario\n"} {
		cfg, err := Parse([]byte(data), env(nil))
		if err != nil {
			t.Fatalf("Parse(%q): %v", data, err)
		}
		if len(cfg.Hosts) != 0 || len(cfg.Services) != 0 {
			t.Errorf("Parse(%q) = %+v, quería vacío", data, cfg)
		}
	}
}

func TestParseErrores(t *testing.T) {
	vars := env(map[string]string{"T": "token"})
	tests := []struct {
		name string
		data string
		want []string // fragmentos que tiene que tener el error
	}{
		{
			name: "clave desconocida arriba",
			data: "hostz:\n  vm: {agent: http://x, token: t}\n",
			want: []string{"hostz", "line 1"},
		},
		{
			name: "token de la API corto",
			data: "api_token: ${T}\n",
			want: []string{"api_token", "32"},
		},
		{
			name: "clave desconocida en un servicio",
			data: "services:\n  router:\n    nmae: Router\n",
			want: []string{"nmae", "line 3"},
		},
		{
			name: "variable sin definir",
			data: "hosts:\n  vm:\n    agent: http://x\n    token: ${NO_EXISTE}\n",
			want: []string{"NO_EXISTE", "línea 4"},
		},
		{
			name: "varias variables sin definir, todas en el error",
			data: "hosts:\n  vm: {agent: \"http://${A}\", token: \"${B}\"}\n",
			want: []string{"A", "B"},
		},
		{
			name: "host sin token",
			data: "hosts:\n  vm: {agent: http://x}\n",
			want: []string{"hosts.vm.token"},
		},
		{
			name: "agent que no es URL http",
			data: "hosts:\n  vm: {agent: 192.168.0.11:9101, token: \"${T}\"}\n",
			want: []string{"hosts.vm.agent"},
		},
		{
			name: "host inexistente",
			data: "services:\n  adguard: {host: lxc, unit: a.service}\n",
			want: []string{"services.adguard.host", `"lxc"`},
		},
		{
			name: "unit sin host",
			data: "services:\n  adguard: {unit: a.service}\n",
			want: []string{"services.adguard", "unit necesita host"},
		},
		{
			name: "unit en una clave de compose",
			data: "hosts:\n  vm: {agent: http://x, token: t}\nservices:\n  p/s: {host: vm, unit: a.service}\n",
			want: []string{"services.p/s.host", "services.p/s.unit"},
		},
		{
			name: "check con URL inválida y código imposible",
			data: "services:\n  router: {check: {http: router.lan, expect: 42}}\n",
			want: []string{"services.router.check.http", "services.router.check.expect"},
		},
		{
			name: "expect que no es número",
			data: "services:\n  router: {check: {http: http://r/, expect: doscientos}}\n",
			want: []string{"doscientos"},
		},
		{
			name: "check vacío",
			data: "services:\n  router: {check: {interval: 30s}}\n",
			want: []string{"services.router.check: falta http, tcp o push"},
		},
		{
			name: "expect sin http",
			data: "services:\n  router: {check: {tcp: \"r:80\", expect: 200}}\n",
			want: []string{"expect y redirects son de http"},
		},
		{
			name: "tcp sin puerto, duraciones inválidas",
			data: "services:\n  db: {check: {tcp: db, interval: un rato, timeout: -1s}}\n",
			want: []string{"check.tcp", "check.interval", "check.timeout"},
		},
		{
			name: "token push corto",
			data: "services:\n  restic: {check: {push: corto}}\n",
			want: []string{"check.push", "16 caracteres"},
		},
		{
			name: "token push repetido",
			data: "services:\n  a: {check: {push: 0123456789abcdef}}\n  b: {check: {push: 0123456789abcdef}}\n",
			want: []string{"services.b.check.push", "mismo token que a"},
		},
		{
			name: "YAML roto",
			data: "hosts: [\n",
			want: []string{"yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data), vars)
			if err == nil {
				t.Fatal("Parse no dio error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("el error no menciona %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestTokenNoSeFiltra(t *testing.T) {
	h := Host{Agent: "http://x", Token: "supersecreto"}
	for _, s := range []string{
		fmt.Sprint(h), fmt.Sprintf("%v", h), fmt.Sprintf("%+v", h),
		fmt.Sprint(map[string]Host{"vm": h}),
	} {
		if strings.Contains(s, "supersecreto") {
			t.Errorf("el token aparece en %q", s)
		}
	}

	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "host", h)
	c := Check{Push: "pushsecreto"}
	if strings.Contains(fmt.Sprint(c)+fmt.Sprintf("%+v", c), "pushsecreto") {
		t.Errorf("el token push aparece en %v", c)
	}
	if strings.Contains(buf.String(), "supersecreto") {
		t.Errorf("el token aparece en el log: %s", buf.String())
	}
}

func TestFileServices(t *testing.T) {
	cfg := &Config{Services: map[string]Service{
		"adguard":    {Name: "AdGuard", Host: "vm", Unit: "a.service", Check: &Check{HTTP: "http://a/", Expect: 200}},
		"p/s":        {Alert: ptr(false), Ignore: ptr(true), Separate: ptr(true)},
		"sin-checks": {Group: "Red"},
		"restic":     {Check: &Check{Push: "0123456789abcdef", Interval: "96h"}},
		"db": {Check: &Check{
			HTTP: "https://db/", Redirects: ptr(false), TCP: "db:5432", Interval: "30s", Timeout: "5s",
		}},
	}}
	want := map[string]catalog.FileService{
		"adguard": {
			Name: "AdGuard", Host: "vm", Unit: "a.service",
			Checks: []catalog.Check{{Kind: catalog.CheckHTTP, Target: "http://a/", Expect: 200}},
		},
		"p/s":        {Alert: ptr(false), Ignore: ptr(true), Separate: ptr(true)},
		"sin-checks": {Group: "Red"}, // Checks nil: no pisa los checks de los labels
		"restic": {Checks: []catalog.Check{
			{Kind: catalog.CheckPush, Target: "0123456789abcdef", Interval: 96 * time.Hour},
		}},
		"db": {Checks: []catalog.Check{
			{Kind: catalog.CheckHTTP, Target: "https://db/", NoRedirects: true, Interval: 30 * time.Second, Timeout: 5 * time.Second},
			{Kind: catalog.CheckTCP, Target: "db:5432", Interval: 30 * time.Second, Timeout: 5 * time.Second},
		}},
	}
	if got := cfg.FileServices(); !reflect.DeepEqual(got, want) {
		t.Errorf("FileServices:\n got %#v\nwant %#v", got, want)
	}
}

// El ejemplo del repo tiene que ser válido: es lo primero que alguien copia.
func TestEjemploDelRepo(t *testing.T) {
	data, err := os.ReadFile("../../maestro.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, env(map[string]string{
		"MAESTRO_TOKEN_VM":      "a",
		"MAESTRO_TOKEN_ADGUARD": "b",
		"MAESTRO_PUSH_RESTIC":   "0123456789abcdef",
		"MAESTRO_API_TOKEN":     "0123456789abcdef0123456789abcdef",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) == 0 || len(cfg.Services) == 0 {
		t.Errorf("el ejemplo quedó vacío: %+v", cfg)
	}
}

func TestLoadArchivoInexistente(t *testing.T) {
	if _, err := Load(t.TempDir() + "/no-existe.yaml"); err == nil {
		t.Error("Load de un archivo inexistente no dio error")
	}
}
