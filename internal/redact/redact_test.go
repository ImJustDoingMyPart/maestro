package redact

import (
	"strings"
	"testing"
)

// Los casos imitan las formas de los stacks del server (nombres de
// variables, labels de Homepage, URLs con credenciales) con valores
// inventados.

func TestSecretName(t *testing.T) {
	secret := []string{
		"DB_PASSWORD", "BESZEL_HOMEPAGE_PASSWORD", "SMB_PASSWORD", "MYSQL_ROOT_PASSWD",
		"SECRET_KEY", "DUCKDNS_TOKEN", "BESZEL_AGENT_TOKEN", "MAESTRO_AGENT_TOKEN",
		"IMMICH_API_KEY", "HOMEPAGE_API_KEY", "AIOSTREAMS_AUTH", "TMDB_ACCESS_TOKEN",
		"homepage.widget.password", "homepage.widget.key", "apiKey", "APIKEY",
		"DB_PASS", "ADMIN_PWD", "KEY", "PASSWORD_SALT", "AWS_SECRET_ACCESS_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS", "PRIVATE_KEY", "WIREGUARD_PRIVATEKEY",
		"WEBUI_AUTH", "passphrase", "access_token", "client_secret",
	}
	for _, n := range secret {
		if !SecretName(n) {
			t.Errorf("SecretName(%q) = false, quería true", n)
		}
	}
	plain := []string{
		"TZ", "DB_USERNAME", "DB_DATABASE_NAME", "UPLOAD_LOCATION", "IMMICH_VERSION",
		"USER_UID", "BASE_URL", "DOMAIN", "MONKEY_MODE", "PASSENGER", "KEYBOARD",
		"POSTGRES_PASSWORD_FILE", "homepage.widget.username", "DISABLED_HOSTS",
		"TEMPLATE_URLS", "DATABASE_URI", "image", "author",
	}
	for _, n := range plain {
		if SecretName(n) {
			t.Errorf("SecretName(%q) = true, quería false", n)
		}
	}
}

func TestEnv(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"secreto simple", "DB_PASSWORD=hunter2hunter2", "DB_PASSWORD=" + Mark},
		{"no secreto queda", "TZ=America/Argentina/Buenos_Aires", "TZ=America/Argentina/Buenos_Aires"},
		{"export", "export DUCKDNS_TOKEN=abcd-1234-efgh", "export DUCKDNS_TOKEN=" + Mark},
		{"espacios alrededor del =", "SECRET_KEY = s3cr3tvalue", "SECRET_KEY = " + Mark},
		{"comillas dobles", `SMB_PASSWORD="con espacios y #"`, `SMB_PASSWORD="` + Mark + `"`},
		{"comillas simples", `SMB_PASSWORD='otra clave'`, `SMB_PASSWORD='` + Mark + `'`},
		{"comentario después", "SECRET_KEY=abcdef # el de la app", "SECRET_KEY=" + Mark},
		{"vacío queda", "IMMICH_API_KEY=", "IMMICH_API_KEY="},
		{"booleano queda", "WEBUI_AUTH=true", "WEBUI_AUTH=true"},
		{"comentarios y blancos quedan", "# DB_PASSWORD=viejo\n\nTZ=UTC", "# DB_PASSWORD=" + Mark + "\n\nTZ=UTC"},
		{"usuario:clave como valor", "AIOSTREAMS_AUTH=roberto:claveLarga123", "AIOSTREAMS_AUTH=" + Mark},
		{"URL con clave en variable no secreta", "DATABASE_URI=postgres://app:pgclave@db:5432/app",
			"DATABASE_URI=postgres://app:" + Mark + "@db:5432/app"},
		{"URL sin clave queda", "DATABASE_URI=sqlite:///data/db.sqlite", "DATABASE_URI=sqlite:///data/db.sqlite"},
		{"varias líneas entre comillas", "PRIVATE_KEY=\"-----BEGIN KEY-----\nMIIEabc\n-----END KEY-----\"\nTZ=UTC",
			"PRIVATE_KEY=\"" + Mark + "\"\nTZ=UTC"},
		{"un valor conocido en otra variable", "DB_PASSWORD=pgclave99\nDATABASE_DSN=host=db password=x pgclave99",
			"DB_PASSWORD=" + Mark + "\nDATABASE_DSN=host=db password=" + Mark + " " + Mark},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Redactor
			if got := r.Env(tt.in); got != tt.want {
				t.Errorf("Env:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestCompose(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"referencia queda",
			"      POSTGRES_PASSWORD: ${DB_PASSWORD}",
			"      POSTGRES_PASSWORD: ${DB_PASSWORD}"},
		{"referencia con mensaje queda",
			"      MAESTRO_AGENT_TOKEN: ${MAESTRO_AGENT_TOKEN:?falta en .env}",
			"      MAESTRO_AGENT_TOKEN: ${MAESTRO_AGENT_TOKEN:?falta en .env}"},
		{"literal en mapa",
			"      POSTGRES_PASSWORD: postgres123",
			"      POSTGRES_PASSWORD: " + Mark},
		{"literal entre comillas",
			`      POSTGRES_PASSWORD: "con espacio"`,
			`      POSTGRES_PASSWORD: "` + Mark + `"`},
		{"literal con comentario",
			"      SECRET_KEY: abc123 # cambiar",
			"      SECRET_KEY: " + Mark},
		{"lista con referencia queda",
			"      - API_KEY=${IMMICH_API_KEY}",
			"      - API_KEY=${IMMICH_API_KEY}"},
		{"lista con literal",
			"      - API_KEY=abcdef123",
			"      - API_KEY=" + Mark},
		{"lista entre comillas",
			`      - "API_KEY=abc def"`,
			`      - "API_KEY=` + Mark + `"`},
		{"label de Homepage con referencia queda",
			"      - homepage.widget.password=${BESZEL_HOMEPAGE_PASSWORD}",
			"      - homepage.widget.password=${BESZEL_HOMEPAGE_PASSWORD}"},
		{"label de Homepage con literal",
			"      - homepage.widget.key=0123456789abcdef",
			"      - homepage.widget.key=" + Mark},
		{"label en mapa",
			"      homepage.widget.password: literal1",
			"      homepage.widget.password: " + Mark},
		{"booleano queda",
			"      - WEBUI_AUTH=true",
			"      - WEBUI_AUTH=true"},
		{"pasar la variable sin valor queda",
			"      - SMB_PASSWORD",
			"      - SMB_PASSWORD"},
		{"clave pública ssh también se tapa",
			"      KEY: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample",
			"      KEY: " + Mark},
		{"defecto de una referencia secreta",
			"      DB_PASS: ${DB_PASSWORD:-postgres}",
			"      DB_PASS: ${DB_PASSWORD:-" + Mark + "}"},
		{"defecto de una referencia de nombre neutro bajo clave secreta",
			"      PASSWORD: ${FOO:-hunter2}",
			"      PASSWORD: ${FOO:-" + Mark + "}"},
		{"defecto dentro de un comando",
			"    command: app --db=${DB_PASSWORD:-postgres}",
			"    command: app --db=${DB_PASSWORD:-" + Mark + "}"},
		{"defecto no secreto queda",
			"      TZ: ${TZ:-UTC}",
			"      TZ: ${TZ:-UTC}"},
		{"bloque entero",
			"      PRIVATE_KEY: |\n        -----BEGIN-----\n        abc\n\n        def\n      OTRA: valor",
			"      PRIVATE_KEY: |\n        " + Mark + "\n        " + Mark + "\n\n        " + Mark + "\n      OTRA: valor"},
		{"bloque en una lista",
			"  - secret: >-\n      uno\n      dos\n  - otra: x",
			"  - secret: >-\n      " + Mark + "\n      " + Mark + "\n  - otra: x"},
		{"URL con clave en cualquier clave",
			"      DATABASE_URL: postgres://immich:pgclave@database:5432/immich",
			"      DATABASE_URL: postgres://immich:" + Mark + "@database:5432/immich"},
		{"URL con token como usuario",
			"      - REPO=https://ghp_abc123@github.com/x/y.git",
			"      - REPO=https://" + Mark + "@github.com/x/y.git"},
		{"URL con clave por referencia queda",
			"      DATABASE_URL: postgres://immich:${DB_PASSWORD}@database:5432/immich",
			"      DATABASE_URL: postgres://immich:${DB_PASSWORD}@database:5432/immich"},
		{"query con token",
			`    test: ["CMD", "curl", "-f", "http://localhost:8080/health?token=abcdef123&x=1"]`,
			`    test: ["CMD", "curl", "-f", "http://localhost:8080/health?token=` + Mark + `&x=1"]`},
		{"flag de línea de comandos",
			"    command: server --admin-password=hunter2 --port 80",
			"    command: server --admin-password=" + Mark + " --port 80"},
		{"comentario con un token",
			"    #   curl http://localhost:3001/api/push/x?token=abc12345",
			"    #   curl http://localhost:3001/api/push/x?token=" + Mark},
		{"lo normal queda",
			"services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.11\n    labels:\n      - homepage.group=Media\n    secrets:\n      - db_password",
			"services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.11\n    labels:\n      - homepage.group=Media\n    secrets:\n      - db_password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Redactor
			if got := r.Compose(tt.in); got != tt.want {
				t.Errorf("Compose:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// Un valor secreto del .env se tapa en el compose y en los logs aunque
// aparezca sin su nombre.
func TestKnownValues(t *testing.T) {
	var r Redactor
	r.Env("DB_PASSWORD=pgclave99\nIMMICH_API_KEY=key-0123456789\nTZ=UTC\nSHORT_TOKEN=abc")
	tests := []struct{ in, want string }{
		{"connecting with pgclave99 to db", "connecting with " + Mark + " to db"},
		{"header x-api-key key-0123456789", "header x-api-key " + Mark},
		{"TZ es UTC", "TZ es UTC"},
		// Muy corto para buscarlo suelto: taparía pedazos de cualquier línea.
		{"abc def", "abc def"},
		// La referencia con defecto a una variable censurada del .env se
		// tapa aunque el nombre no parezca secreto.
	}
	for _, tt := range tests {
		if got := r.Text(tt.in); got != tt.want {
			t.Errorf("Text(%q) = %q, quería %q", tt.in, got, tt.want)
		}
	}
	r2 := Redactor{}
	r2.Env("CUSTOM_AUTH=secretísimo")
	if got, want := r2.Compose("      X: ${CUSTOM_AUTH:-otro}"), "      X: ${CUSTOM_AUTH:-"+Mark+"}"; got != want {
		t.Errorf("Compose = %q, quería %q", got, want)
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"línea normal queda", `level=info msg="listening on :8080"`, `level=info msg="listening on :8080"`},
		{"logfmt", `level=error msg="login failed" password=hunter2 user=roberto`,
			`level=error msg="login failed" password=` + Mark + ` user=roberto`},
		{"json", `{"level":"info","token":"abc.def.ghi","user":"x"}`,
			`{"level":"info","token":"` + Mark + `","user":"x"}`},
		{"bearer", "GET /api Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.x.y",
			"GET /api Authorization: Bearer " + Mark},
		{"basic", "Authorization: Basic cm9iZXJ0bzpjbGF2ZQ==", "Authorization: Basic " + Mark},
		{"url con clave", "dial postgres://immich:pg@database:5432 failed",
			"dial postgres://immich:" + Mark + "@database:5432 failed"},
		{"email no es una URL", "mail from roberto@example.com", "mail from roberto@example.com"},
		{"query", "GET /push/x?status=up&apikey=0123abcd", "GET /push/x?status=up&apikey=" + Mark},
		{"hora no es una asignación", "2026-11-11T12:30:45Z started", "2026-11-11T12:30:45Z started"},
		{"palabra key en texto", "cache key: users:42", "cache key: " + Mark},
		{"booleano queda", "auth=false", "auth=false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Redactor
			if got := r.Text(tt.in); got != tt.want {
				t.Errorf("Text:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// Ningún valor secreto del .env sobrevive en el compose, por más que esté
// escrito en cualquier lado.
func TestComposeNoLeak(t *testing.T) {
	env := "DB_PASSWORD=Zq8-unicoValor\nTZ=UTC"
	compose := strings.Join([]string{
		"services:",
		"  db:",
		"    image: postgres:16",
		"    environment:",
		"      POSTGRES_PASSWORD: ${DB_PASSWORD}",
		"      PGPASS_COPY: Zq8-unicoValor",
		"      DSN: host=db dbname=x sslmode=disable Zq8-unicoValor",
		"    healthcheck:",
		`      test: ["CMD-SHELL", "PGPASSWORD=Zq8-unicoValor pg_isready"]`,
	}, "\n")
	var r Redactor
	r.Env(env)
	if out := r.Compose(compose); strings.Contains(out, "Zq8-unicoValor") {
		t.Errorf("se escapó el secreto:\n%s", out)
	}
}
