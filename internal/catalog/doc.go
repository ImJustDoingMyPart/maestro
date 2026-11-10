// Package catalog arma la lista de servicios combinando las fuentes: labels de
// compose, labels homepage.* y maestro.*, y el archivo maestro.yaml. No toca
// Docker ni la red: es una función pura sobre datos ya leídos.
package catalog
