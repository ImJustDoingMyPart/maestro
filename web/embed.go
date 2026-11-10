// Package web embebe las plantillas y los archivos estáticos en el binario.
//
// El JavaScript propio se escribe en src/ y esbuild lo junta en
// static/gen/. esbuild está escrito en Go y figura como herramienta en
// go.mod: no hace falta Node. La salida se commitea, así un go build
// alcanza; después de tocar src/ hay que correr go generate ./web.
package web

//go:generate go tool esbuild src/logs.js --bundle --minify --format=iife --target=es2020,safari15 --legal-comments=none --outfile=static/gen/logs.js

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
