// Command sveltebuild arma el bundle de las páginas hechas con Svelte.
//
// Es esbuild (el mismo de go.mod) con un plugin para los archivos .svelte:
// cada uno lo compila web/svelte-compile.mjs con el compilador de Svelte,
// que está escrito en JavaScript y necesita Node. Node hace falta solo
// para go generate: la salida se commitea y go build no lo usa.
//
// Sale en módulos ES con code splitting: lo que se importa con import()
// (el editor de Compose con CodeMirror, los gráficos con uPlot) queda en un
// chunk aparte que el navegador baja solo al usarlo. La carpeta de salida
// se vacía antes: los chunks llevan un hash en el nombre y los viejos no
// tienen que quedar embebidos.
//
// Uso (desde web/, como lo llama //go:generate):
//
//	sveltebuild -outdir static/gen/app src/app/main.js
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	outdir := flag.String("outdir", "static/gen/app", "carpeta de salida")
	flag.Parse()
	if err := os.RemoveAll(*outdir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "uso: sveltebuild -outdir <carpeta> <entrada.js>...")
		os.Exit(2)
	}

	svelte := api.Plugin{
		Name: "svelte",
		Setup: func(b api.PluginBuild) {
			b.OnLoad(api.OnLoadOptions{Filter: `\.svelte$`}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				var out, errb bytes.Buffer
				cmd := exec.Command("node", "svelte-compile.mjs", args.Path)
				cmd.Stdout, cmd.Stderr = &out, &errb
				if err := cmd.Run(); err != nil {
					return api.OnLoadResult{}, fmt.Errorf("svelte %s: %v\n%s", args.Path, err, errb.String())
				}
				js := out.String()
				warnings := warningsOf(errb.String())
				return api.OnLoadResult{Contents: &js, Loader: api.LoaderJS, Warnings: warnings}, nil
			})
		},
	}

	res := api.Build(api.BuildOptions{
		EntryPoints:       flag.Args(),
		Bundle:            true,
		Outdir:            *outdir,
		Write:             true,
		Format:            api.FormatESModule,
		Splitting:         true,
		EntryNames:        "[name]",
		ChunkNames:        "chunk-[hash]",
		Platform:          api.PlatformBrowser,
		Target:            api.ES2022,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsNone,
		Conditions:        []string{"svelte", "browser", "production"},
		Plugins:           []api.Plugin{svelte},
		LogLevel:          api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		os.Exit(1)
	}
}

var lineRe = regexp.MustCompile(`(?m)^.+$`)

// warningsOf pasa los avisos del compilador (uno por renglón) a esbuild,
// que los muestra con los suyos.
func warningsOf(s string) []api.Message {
	var out []api.Message
	for _, l := range lineRe.FindAllString(strings.TrimSpace(s), -1) {
		out = append(out, api.Message{Text: l})
	}
	return out
}
