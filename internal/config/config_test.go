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
  immich/redis:
    hidden: true
stacks:
  vm/immich: { name: Fotos, group: Archivos, hidden: false }
groups: [Archivos, Red]
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
			"immich/redis":         {Hidden: ptr(true)},
		},
		Stacks: map[string]Stack{"vm/immich": {Name: "Fotos", Group: "Archivos", Hidden: ptr(false)}},
		Groups: []string{"Archivos", "Red"},
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
			name: "telegram con token y chat que no tienen la forma",
			data: "telegram: { token: \"${T}\", chat: \"123, juan\" }\n",
			want: []string{"telegram.token", "telegram.chat: el destino 2"},
		},
		{
			name: "telegram sin chat",
			data: "telegram: { token: \"123:abcdefghijklmnopqrstuvwxyz\" }\n",
			want: []string{"telegram.chat: falta"},
		},
		{
			name: "public_url relativa",
			data: "public_url: /maestro\n",
			want: []string{"public_url"},
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
			name: "stacks con clave mala y host inexistente",
			data: "stacks:\n  immich: {name: Fotos}\n  lxc/web: {hidden: true}\n",
			want: []string{"stacks.immich: la clave va como", `stacks.lxc/web: el host "lxc"`},
		},
		{
			name: "groups vacío, repetido y Otros",
			data: "groups: [Red, \"\", Red, otros]\n",
			want: []string{"groups[1]: nombre vacío", `"Red" está repetido`, "va siempre al final"},
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

func TestTelegram(t *testing.T) {
	cfg, err := Parse([]byte("telegram:\n  token: ${TG_TOKEN}\n  chat: ${TG_CHAT}\npublic_url: https://maestro.example.org\n"),
		env(map[string]string{"TG_TOKEN": "123456:ABCdef_ghi-jklmnopqrstuvwxyz", "TG_CHAT": " 1418192704, -1001234567890 @avisos "}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Telegram.Chats(), []string{"1418192704", "-1001234567890", "@avisos"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Chats() = %q, quiero %q", got, want)
	}
	if cfg.PublicURL != "https://maestro.example.org" {
		t.Errorf("PublicURL = %q", cfg.PublicURL)
	}

	// Ni el token ni los chats salen en un print o en el log.
	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "telegram", *cfg.Telegram)
	for _, s := range []string{fmt.Sprint(*cfg.Telegram), fmt.Sprintf("%+v", *cfg.Telegram), buf.String()} {
		if strings.Contains(s, "ABCdef") || strings.Contains(s, "1418192704") {
			t.Errorf("se filtra un secreto en %q", s)
		}
	}
}

func TestFileServices(t *testing.T) {
	cfg := &Config{Services: map[string]Service{
		"adguard":    {Name: "AdGuard", Host: "vm", Unit: "a.service", Check: &Check{HTTP: "http://a/", Expect: 200}},
		"p/s":        {Alert: ptr(false), Ignore: ptr(true), Separate: ptr(true), Hidden: ptr(true)},
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
		"p/s":        {Alert: ptr(false), Ignore: ptr(true), Separate: ptr(true), Hidden: ptr(true)},
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
		"MAESTRO_TOKEN_VM":       "a",
		"MAESTRO_TOKEN_ADGUARD":  "b",
		"MAESTRO_PUSH_RESTIC":    "0123456789abcdef",
		"MAESTRO_API_TOKEN":      "0123456789abcdef0123456789abcdef",
		"MAESTRO_TELEGRAM_TOKEN": "123456:ABCdefghijklmnopqrstuvwxyz",
		"MAESTRO_TELEGRAM_CHAT":  "1418192704",
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
