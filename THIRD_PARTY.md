# Código y recursos de terceros

Maestro es MIT (ver `LICENSE`). El binario lleva embebido esto, cada uno con su licencia:

| Qué | Dónde | Licencia |
| --- | --- | --- |
| Beer CSS 5.0.3 | `web/static/beercss-5.0.3` | MIT |
| Material Symbols (la fuente de íconos que trae Beer CSS) | `web/static/beercss-5.0.3/*.woff2` | Apache-2.0 |
| Google Sans Flex 5.3.2 | `web/static/fonts/google-sans-flex-5.3.2` | SIL Open Font License 1.1 (`OFL.txt`) |
| SortableJS 1.15.7 | `web/static/sortable-1.15.7.min.js` | MIT |
| Svelte, CodeMirror 6, uPlot (en el bundle de `web/static/gen/app`) | versiones en `web/npm.lock` | MIT |

Las dependencias de Go están en `go.mod`, cada una con la licencia de su módulo (Apache-2.0, BSD o MIT).

Los logos de los servicios no vienen con Maestro: el hub los baja la primera vez que hacen falta y los guarda al lado de la base. Salen de [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) (Apache-2.0), [selfh.st/icons](https://github.com/selfhst/icons) (CC BY 4.0), [Material Design Icons](https://github.com/Templarian/MaterialDesign) (Pictogrammers Free License) y [Simple Icons](https://github.com/simple-icons/simple-icons) (CC0). Las marcas son de sus dueños.
