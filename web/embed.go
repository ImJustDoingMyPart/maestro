// Package web embebe las plantillas y los archivos estáticos en el binario.
//
// La interfaz es una aplicación de Svelte (src/app/, más los módulos de
// src/ que usa: el visor de logs, el editor de Compose, los gráficos). La
// arma internal/sveltebuild, que es esbuild (en Go, herramienta de go.mod)
// más el compilador de Svelte, que necesita Node solo para go generate. Sale
// a static/gen/app/: main.js, main.css y los chunks que se cargan aparte.
// Los paquetes de npm (Svelte, CodeMirror, uPlot) los baja npmfetch a
// node_modules/ con las versiones de npm.lock. La salida se commitea, así
// un go build alcanza; después de tocar src/ o npm.lock hay que correr
// go generate ./web.
package web

//go:generate go run ../internal/npmfetch -lock npm.lock -dir node_modules
//go:generate go run ../internal/sveltebuild -outdir static/gen/app src/app/main.js

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
