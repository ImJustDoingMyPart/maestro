// Package web embebe los archivos estáticos (y, desde el paso 2, las
// plantillas) en el binario.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static son los archivos servidos tal cual (CSS, JS, fuentes), con rutas
// relativas a static/.
var Static = mustSub("static")

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic(err) // solo falla si dir no es una ruta válida: error de programación
	}
	return sub
}
