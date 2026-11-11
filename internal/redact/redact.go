// Package redact censura secretos en el compose, el .env y el texto libre
// (logs, motivos, URLs) que van al informe para IA. El informe está hecho
// para salir del servidor, así que la censura se equivoca a favor de
// censurar: un valor de más tapado es una molestia; uno de menos, un
// secreto filtrado.
//
// No hay modo sin censura: quien necesita el valor real lo ve en la pestaña
// Compose, con sesión.
package redact

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Mark reemplaza a cada valor censurado.
const Mark = "[censurado]"

// minKnownLen es el largo mínimo de un valor del .env para buscarlo en el
// resto del texto. Uno más corto taparía pedazos de cualquier línea.
const minKnownLen = 4

// Redactor censura un stack: primero el .env (Env), que le enseña los
// valores secretos, y después el compose (Compose) y el texto libre (Text),
// donde esos valores se tapan aunque aparezcan sin su nombre. El valor cero
// sirve, sin .env.
//
// No es seguro para varias goroutines: Env cambia lo que sabe.
type Redactor struct {
	known []string        // valores secretos del .env, del más largo al más corto
	names map[string]bool // variables del .env censuradas
}

// SecretName dice si el nombre de una variable, clave o parámetro parece de
// un secreto: contiene password, passwd, passphrase, secret, token,
// credential, private o apikey, o tiene una palabra pass, pwd, key, auth o
// salt (las palabras se separan por _, ., -, espacios y mayúsculas:
// "DB_PASS", "homepage.widget.key", "apiKey"). Los que terminan en _FILE
// son rutas (la convención de Docker para los secretos en archivo), no
// secretos.
func SecretName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "_file") {
		return false
	}
	for _, s := range []string{"password", "passwd", "passphrase", "secret", "token", "credential", "private", "apikey"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	for _, w := range words(name) {
		switch w {
		case "pass", "pwd", "key", "auth", "salt":
			return true
		}
	}
	return false
}

// words parte un nombre en palabras en minúsculas: por separadores y por el
// paso de minúscula a mayúscula ("apiKey" → api, key).
func words(name string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	prevLower := false
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
			prevLower = false
			continue
		case unicode.IsUpper(r) && prevLower:
			flush()
		}
		cur = append(cur, r)
		prevLower = unicode.IsLower(r)
	}
	flush()
	return out
}

// harmless son valores que no se censuran aunque el nombre parezca de
// secreto: WEBUI_AUTH=true no esconde nada, y taparlo esconde información.
func harmless(v string) bool {
	switch strings.ToLower(v) {
	case "", "true", "false", "yes", "no", "on", "off", "0", "1", "null", "none", "~", "''", `""`:
		return true
	}
	return false
}

// learn agrega un valor secreto para buscarlo en el resto del texto.
func (r *Redactor) learn(name, value string) {
	if r.names == nil {
		r.names = map[string]bool{}
	}
	r.names[name] = true
	if len(value) < minKnownLen || slices.Contains(r.known, value) {
		return
	}
	r.known = append(r.known, value)
	// El más largo primero: si uno contiene a otro, se tapa entero.
	slices.SortStableFunc(r.known, func(a, b string) int { return len(b) - len(a) })
}

// envLine es KEY=valor, con export opcional adelante.
var envLine = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(.*)$`)

// Env censura un .env: el valor de cada variable con nombre de secreto
// (SecretName), aunque ocupe varias líneas entre comillas. Los demás valores
// pasan por Text. Los valores censurados quedan aprendidos para Compose y
// Text.
func (r *Redactor) Env(content string) string {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		m := envLine.FindStringSubmatch(lines[i])
		if m == nil {
			lines[i] = r.Text(lines[i])
			continue
		}
		prefix, name, eq, value := m[1], m[2], m[3], m[4]
		if !SecretName(name) {
			lines[i] = r.Text(lines[i])
			continue
		}
		raw := strings.TrimSpace(value)
		q := quote(raw)
		if q == 0 {
			// Sin comillas, un " #" empieza un comentario.
			if c := strings.Index(raw, " #"); c >= 0 {
				raw = strings.TrimSpace(raw[:c])
			}
			if !harmless(raw) {
				r.learn(name, raw)
				lines[i] = prefix + name + eq + Mark
			}
			continue
		}
		// Entre comillas: puede seguir en las líneas de abajo hasta que
		// cierre.
		body, closed := raw[1:], false
		if end := strings.IndexByte(body, q); end >= 0 {
			body, closed = body[:end], true
		}
		j := i
		for !closed && j+1 < len(lines) {
			j++
			if end := strings.IndexByte(lines[j], q); end >= 0 {
				body += "\n" + lines[j][:end]
				closed = true
			} else {
				body += "\n" + lines[j]
			}
		}
		if harmless(body) {
			continue
		}
		for _, part := range strings.Split(body, "\n") {
			r.learn(name, strings.TrimSpace(part))
		}
		lines[i] = prefix + name + eq + string(q) + Mark + string(q)
		lines = slices.Delete(lines, i+1, j+1)
	}
	return strings.Join(lines, "\n")
}

// quote devuelve la comilla con la que empieza v, o 0.
func quote(v string) byte {
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		return v[0]
	}
	return 0
}

// composeEntry es una entrada de un mapa ("KEY: valor", también dentro de
// una lista) o de una lista de variables ("- KEY=valor", con o sin comillas
// alrededor).
var (
	composeMap  = regexp.MustCompile(`^(\s*(?:-\s+)?)(["']?)([A-Za-z_][A-Za-z0-9_.\-/]*)(["']?)(\s*:)(\s+(.*))?$`)
	composeList = regexp.MustCompile(`^(\s*-\s+)(["']?)([A-Za-z_][A-Za-z0-9_.\-/]*)=(.*)$`)
)

// Compose censura un compose.yml línea por línea, sin parsearlo, para que
// el resultado se lea igual que el archivo (comentarios incluidos):
//
//   - el valor de una clave o variable con nombre de secreto, salvo que sea
//     solo una referencia (${VAR}, ${VAR:?mensaje}, $VAR), que no dice
//     nada; un bloque (| o >) se tapa entero;
//   - el valor por defecto de ${VAR:-defecto} si VAR parece secreto o está
//     censurada en el .env;
//   - todo lo que tapa Text.
func (r *Redactor) Compose(content string) string {
	lines := strings.Split(content, "\n")
	blockIndent := -1 // indentación de la clave cuyo bloque se está tapando
	for i, line := range lines {
		if blockIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent(line) > blockIndent {
				if strings.TrimSpace(line) != "" {
					lines[i] = line[:indent(line)] + Mark
				}
				continue
			}
			blockIndent = -1
		}
		if m := composeList.FindStringSubmatch(line); m != nil && SecretName(m[3]) {
			q, value := m[2], strings.TrimSpace(m[4])
			if q != "" {
				value = strings.TrimSuffix(value, q)
			}
			if !reference(value) && !harmless(value) {
				lines[i] = m[1] + q + m[3] + "=" + Mark + q
				continue
			}
		} else if m := composeMap.FindStringSubmatch(line); m != nil && m[6] != "" && SecretName(m[3]) {
			value := strings.TrimSpace(stripComment(m[7]))
			switch {
			case strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">"):
				// El bloque va más adentro que la clave (que en una lista
				// está después del "- ").
				blockIndent = len(m[1])
				continue
			case defaultRe.MatchString(unquote(value)) && defaultRe.FindString(unquote(value)) == unquote(value):
				// ${VAR:-defecto} entero: queda la referencia, se tapa el
				// defecto aunque VAR no parezca secreto.
				sub := defaultRe.FindStringSubmatch(unquote(value))
				if harmless(sub[3]) {
					break
				}
				q := ""
				if c := quote(value); c != 0 {
					q = string(c)
				}
				lines[i] = m[1] + m[2] + m[3] + m[4] + m[5] + " " + q + "${" + sub[1] + sub[2] + Mark + "}" + q
				continue
			case !reference(unquote(value)) && !harmless(unquote(value)):
				q := ""
				if c := quote(value); c != 0 {
					q = string(c)
				}
				lines[i] = m[1] + m[2] + m[3] + m[4] + m[5] + " " + q + Mark + q
				continue
			}
		}
		lines[i] = r.Text(line)
	}
	return strings.Join(lines, "\n")
}

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }

// stripComment saca un comentario de YAML (" #" fuera de comillas).
func stripComment(v string) string {
	var q rune
	for i, c := range v {
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return v[:i]
		}
	}
	return v
}

func unquote(v string) string {
	if len(v) >= 2 && quote(v) != 0 && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// reference reconoce un valor que es solo una referencia a una variable sin
// valor por defecto: ${VAR}, ${VAR:?mensaje}, ${VAR?mensaje} o $VAR.
var referenceRe = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*(?::?\?[^}]*)?\})$`)

func reference(v string) bool { return referenceRe.MatchString(v) }

var (
	// ${VAR:-defecto}, ${VAR-defecto}, ${VAR:=defecto}, ${VAR:+alternativa}
	defaultRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:?[-=+])([^}]*)\}`)
	// esquema://usuario:clave@ y esquema://token@
	urlCredRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)([^/\s:@'"]*)(:[^/\s@'"]*)?@`)
	// ?nombre=valor y &nombre=valor
	queryRe = regexp.MustCompile(`([?&;])([^=&\s?#]+)=([^&\s#"']+)`)
	// Authorization: Bearer xxx / Basic xxx
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)([A-Za-z0-9._~+/=\-]{8,})`)
	// nombre=valor, nombre: valor, "nombre":"valor". El valor no empieza
	// con "$" (una referencia), "[" (ya censurado) ni "-", "?", "=" o "+"
	// (el resto de ${VAR:-…} o ${VAR:?…}).
	assignRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.\-]*)("?\s*[=:]\s*"?)([^\s"',;&}\-$\[?=+][^\s"',;&}]*)`)
)

// Text censura texto libre (una línea de log, un motivo, una URL): los
// valores secretos que aprendió Env, credenciales en URLs, parámetros de
// query y asignaciones con nombre de secreto (password=…, "token":"…"),
// valores por defecto secretos de ${VAR:-…} y tokens de Authorization.
func (r *Redactor) Text(s string) string {
	for _, v := range r.known {
		s = strings.ReplaceAll(s, v, Mark)
	}
	s = defaultRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := defaultRe.FindStringSubmatch(m)
		if (SecretName(sub[1]) || r.names[sub[1]]) && !harmless(sub[3]) && !reference(sub[3]) {
			return "${" + sub[1] + sub[2] + Mark + "}"
		}
		return m
	})
	s = urlCredRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := urlCredRe.FindStringSubmatch(m)
		if sub[3] == "" { // solo usuario: puede ser un token (https://<token>@github.com)
			if sub[2] == "" || reference(sub[2]) {
				return m
			}
			return sub[1] + Mark + "@"
		}
		if reference(sub[3][1:]) {
			return m
		}
		return sub[1] + sub[2] + ":" + Mark + "@"
	})
	s = queryRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := queryRe.FindStringSubmatch(m)
		if SecretName(sub[2]) && !harmless(sub[3]) && !reference(sub[3]) {
			return sub[1] + sub[2] + "=" + Mark
		}
		return m
	})
	s = bearerRe.ReplaceAllString(s, "${1}${2}"+Mark)
	s = assignRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := assignRe.FindStringSubmatch(m)
		if SecretName(sub[1]) && !harmless(sub[3]) {
			return sub[1] + sub[2] + Mark
		}
		return m
	})
	return s
}
