package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// La coherencia visual no depende de acordarse: maestro.css saca todas sus
// medidas de la escala de :root (criterio en PLAN.md, "Coherencia visual").
// Estos tests fallan si alguien escribe un número suelto donde va un token.

// tokenBlocks son las reglas donde se definen los tokens: ahí, y solo ahí,
// van valores literales.
var tokenBlocks = map[string]bool{":root": true, "body.light": true, "body.dark": true}

// decl es una declaración con la regla y la línea donde está.
type decl struct {
	selector string
	prop     string
	value    string
	line     int
}

// parseCSS devuelve las declaraciones de una hoja con el selector de la
// regla que las contiene (el más interno, si hay @media). Alcanza para la
// hoja propia: no entiende strings con llaves ni @import.
func parseCSS(src string) []decl {
	// Los comentarios se cambian por espacios del mismo largo, salvo los
	// saltos de línea, así los números de línea siguen valiendo.
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllStringFunc(src, func(c string) string {
		return regexp.MustCompile(`[^\n]`).ReplaceAllString(c, " ")
	})
	var (
		out   []decl
		stack []string
		buf   strings.Builder
		start = 1 // línea donde empieza lo que hay en buf
		line  = 1
	)
	flush := func() {
		text := buf.String()
		buf.Reset()
		if len(stack) == 0 {
			return
		}
		sel := stack[len(stack)-1]
		l := start
		for _, part := range strings.Split(text, ";") {
			ln := l + strings.Count(part[:len(part)-len(strings.TrimLeft(part, " \t\n"))], "\n")
			l += strings.Count(part, "\n")
			prop, val, ok := strings.Cut(part, ":")
			if !ok {
				continue
			}
			out = append(out, decl{sel, strings.TrimSpace(prop), strings.TrimSpace(val), ln})
		}
	}
	for _, r := range src {
		switch r {
		case '{':
			stack = append(stack, strings.Join(strings.Fields(buf.String()), " "))
			buf.Reset()
			start = line
		case '}':
			flush()
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			start = line
		default:
			if r == '\n' {
				line++
			}
			buf.WriteRune(r)
		}
	}
	return out
}

var (
	// Un número con unidad: 1rem, 0.5px, 12%, 0.2s. "0" solo y los
	// coeficientes sin unidad de un calc (-1 * …) no cuentan.
	lengthRe = regexp.MustCompile(`(^|[^\w-])-?(\d*\.)?\d+(rem|em|px|%|s|ms)\b`)
	hexRe    = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	colorFn  = regexp.MustCompile(`\b(rgba?|hsla?|oklch|lab)\(`)
	mixPct   = regexp.MustCompile(`color-mix\([^;]*\d%`)
	varRe    = regexp.MustCompile(`var\((--[\w-]+)`)
)

// scaled dice si una propiedad tiene que tomar su valor de la escala.
func scaled(prop string) bool {
	switch {
	case prop == "gap", prop == "row-gap", prop == "column-gap",
		strings.HasPrefix(prop, "padding"), strings.HasPrefix(prop, "margin"),
		strings.HasPrefix(prop, "border") && strings.HasSuffix(prop, "radius"),
		prop == "font", prop == "font-size",
		strings.HasPrefix(prop, "transition"), strings.HasPrefix(prop, "animation"):
		return true
	}
	return false
}

func readCSS(t *testing.T) (string, []decl) {
	t.Helper()
	b, err := os.ReadFile("static/maestro.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b), parseCSS(string(b))
}

func TestEstiloSinValoresSueltos(t *testing.T) {
	_, decls := readCSS(t)
	for _, d := range decls {
		if tokenBlocks[d.selector] || strings.HasPrefix(d.prop, "--") || strings.HasPrefix(d.selector, "@font-face") {
			continue
		}
		where := func(msg string) {
			t.Errorf("maestro.css:%d: %s { %s: %s }: %s", d.line, d.selector, d.prop, d.value, msg)
		}
		if hexRe.MatchString(d.value) || colorFn.MatchString(d.value) {
			where("color escrito a mano; va una variable de M3 o un token de :root")
		}
		if mixPct.MatchString(d.value) {
			where("porcentaje de color-mix escrito a mano; va un token --a-*")
		}
		if scaled(d.prop) && lengthRe.MatchString(d.value) {
			where("medida suelta; va un token de la escala (--sp-*, --r-*, --type-*, --icon-*, --dur-*)")
		}
		if d.prop == "opacity" && d.value != "0" && d.value != "1" && !strings.HasPrefix(d.value, "var(") {
			where("opacidad suelta; va un token --a-*")
		}
	}
}

// Un token mal escrito (var(--sp-5), que no existe) no falla en el
// navegador: la propiedad queda sin valor y se ve mal sin aviso.
func TestEstiloTokensDefinidos(t *testing.T) {
	_, decls := readCSS(t)
	defined := map[string]bool{}
	for _, d := range decls {
		if strings.HasPrefix(d.prop, "--") {
			defined[d.prop] = true
		}
	}
	// Las de M3 las define Beer CSS; las propias son las de estos prefijos.
	own := regexp.MustCompile(`^--(sp|r|type|icon|a|dur|ease|font|mono|status|state|cols|gap|hw)\b`)
	for _, d := range decls {
		for _, m := range varRe.FindAllStringSubmatch(d.value, -1) {
			if own.MatchString(m[1]) && !defined[m[1]] {
				t.Errorf("maestro.css:%d: %s usa %s, que no está definida", d.line, d.selector, m[1])
			}
		}
	}
}

// Las plantillas y los componentes no llevan estilos en línea: un style="" es una medida
// fuera de la escala. Se permite solo con un valor que sale de los datos
// (el ancho de una barra de uso).
func TestPlantillasSinEstilosEnLinea(t *testing.T) {
	var files []string
	for _, pat := range []string{"templates/*.html", "src/app/*.svelte", "src/app/*/*.svelte"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	style := regexp.MustCompile(`style="([^"]*)"`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(b), "\n") {
			for _, m := range style.FindAllStringSubmatch(ln, -1) {
				if !strings.Contains(m[1], "{{") {
					t.Errorf("%s:%d: style=%q: va una clase en maestro.css", f, i+1, m[1])
				}
			}
		}
	}
}

// El parser tiene que ver los selectores y las líneas bien, o los otros
// tests no prueban nada.
func TestParseCSS(t *testing.T) {
	src := "/* a { b: c } */\n:root { --x: 1px; }\n@media (x) {\n  .a, .b {\n    gap: 1rem;\n  }\n}\n.c { margin: 0 }"
	got := parseCSS(src)
	want := []decl{
		{":root", "--x", "1px", 2},
		{".a, .b", "gap", "1rem", 5},
		{".c", "margin", "0", 8},
	}
	if len(got) != len(want) {
		t.Fatalf("parseCSS: %d declaraciones, quería %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseCSS[%d] = %+v, quería %+v", i, got[i], want[i])
		}
	}
	for _, c := range []struct {
		v   string
		hit bool
	}{
		{"var(--sp-2)", false}, {"0", false}, {"calc(-1 * var(--sp-2))", false},
		{"0.5rem", true}, {"2px", true}, {"opacity 0.15s", true}, {"0 1rem", true},
		{"var(--sp-1) 12px", true}, {"inherit", false},
	} {
		if lengthRe.MatchString(c.v) != c.hit {
			t.Errorf("lengthRe(%q) = %v, quería %v", c.v, !c.hit, c.hit)
		}
	}
}
