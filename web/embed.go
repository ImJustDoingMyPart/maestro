// Package web embebe las plantillas y los archivos estáticos en el binario.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var files embed.FS

// Templates son las plantillas html/template, con rutas relativas a templates/.
var Templates = mustSub("templates")

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
