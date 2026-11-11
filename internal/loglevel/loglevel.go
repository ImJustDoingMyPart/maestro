// Package loglevel detecta el nivel de una línea de log con las formas más
// comunes: palabras en mayúsculas (ERROR, WARN), logfmt (level=error), JSON
// ("level":"error"), corchetes ([error]) y los niveles numéricos de pino
// (50 = error, 40 = warn). No es exacto: es una ayuda para filtrar.
//
// Es el port de web/src/level.js, que usa el visor: los dos tienen los
// mismos casos de test y tienen que cambiar juntos.
package loglevel

import (
	"regexp"
	"strconv"
	"strings"
)

// Level es el nivel de una línea.
type Level int

const (
	Info Level = iota
	Warn
	Error
)

var (
	// Las palabras sueltas solo cuentan en mayúsculas: "0 errors" o "error
	// handling" en minúsculas suelen ser texto, no el nivel.
	errorWord = regexp.MustCompile(`\b(?:ERROR|ERR|FATAL|CRIT|CRITICAL|PANIC|EMERG|ALERT)\b`)
	warnWord  = regexp.MustCompile(`\b(?:WARN|WARNING)\b`)
	// level=error, lvl=eror (Grafana), "level":"error", severity: "ERROR", [error]
	kv = regexp.MustCompile(`(?i)(?:\b(?:level|lvl|severity|loglevel)"?\s*[=:]\s*"?|\[)(error|eror|err|fatal|crit|critical|panic|emerg|alert|warn|warning)\b`)
	// pino: "level":50
	pino = regexp.MustCompile(`"level"\s*:\s*(\d{2})\b`)
	// ANSI: colores y movimientos del cursor que escriben muchas imágenes.
	ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
)

// StripANSI saca los códigos de escape ANSI.
func StripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansi.ReplaceAllString(s, "")
}

// Detect da el nivel de una línea (ya sin ANSI).
func Detect(s string) Level {
	if m := kv.FindStringSubmatch(s); m != nil {
		if strings.HasPrefix(strings.ToLower(m[1]), "warn") {
			return Warn
		}
		return Error
	}
	if m := pino.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch {
		case n >= 50:
			return Error
		case n >= 40:
			return Warn
		}
		return Info
	}
	if errorWord.MatchString(s) {
		return Error
	}
	if warnWord.MatchString(s) {
		return Warn
	}
	return Info
}
