// Package web embebe las plantillas y los archivos estáticos en el binario.
//
// El JavaScript propio se escribe en src/ y esbuild lo junta en
// static/gen/, un archivo por página (logs.js, compose.js). esbuild está
// escrito en Go y figura como herramienta en go.mod: no hace falta Node. Los
// paquetes de npm que usa el editor (CodeMirror) los baja npmfetch a
// node_modules/ con las versiones de npm.lock. La salida se commitea, así
// un go build alcanza; después de tocar src/ o npm.lock hay que correr
// go generate ./web. La portada está hecha con Svelte (src/portada/): la
// arma internal/sveltebuild, que es esbuild más el compilador de Svelte, y
// ese compilador sí necesita Node (solo para go generate).
package web

//go:generate go run ../internal/npmfetch -lock npm.lock -dir node_modules
//go:generate go run ../internal/sveltebuild -outdir static/gen src/portada.js
//go:generate go tool esbuild src/logs.js src/compose.js src/charts.js --bundle --minify --format=iife --target=es2020,safari15 --legal-comments=none --outdir=static/gen

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
