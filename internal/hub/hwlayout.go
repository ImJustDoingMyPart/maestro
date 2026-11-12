package hub

import (
	"cmp"
	"context"
	"net/http"
	"regexp"
	"slices"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// hwLayoutStore guarda el orden y los ocultos de la pantalla Hardware. Lo
// cumple *store.Store; sin él (tests con prefs falsas), Hardware no tiene
// modo edición.
type hwLayoutStore interface {
	HWLayout(ctx context.Context, host string) (map[string]store.HWItem, error)
	SetHWOrder(ctx context.Context, host string, items []string) error
	SetHWHidden(ctx context.Context, host, item string, hidden bool) error
	HWPinned(ctx context.Context) (string, error)
	SetHWPinned(ctx context.Context, host string) error
}

// hwItem es la clave de un elemento de Hardware: "tile:cpu", "chart:mem",
// "section:storage".
var hwItem = regexp.MustCompile(`^(tile|chart|section):[a-z0-9-]{1,40}$`)

// applyHWLayout ordena los mosaicos y los gráficos de un host y marca lo
// oculto. Lo que tiene posición va primero, en ese orden; lo demás
// después, en el orden de siempre.
func applyHWLayout(h *hostHardware, layout map[string]store.HWItem) {
	pos := func(id string) int {
		if it, ok := layout[id]; ok && it.Position >= 0 {
			return it.Position
		}
		return 1 << 30
	}
	slices.SortStableFunc(h.Tiles, func(a, b tile) int { return cmp.Compare(pos(a.ID), pos(b.ID)) })
	slices.SortStableFunc(h.Charts, func(a, b chartSpec) int { return cmp.Compare(pos(a.ID), pos(b.ID)) })
	hidden := func(id string) bool { return layout[id].Hidden }
	for i := range h.Tiles {
		h.Tiles[i].Hidden = hidden(h.Tiles[i].ID)
	}
	for i := range h.Charts {
		h.Charts[i].Hidden = hidden(h.Charts[i].ID)
	}
	if h.FSChart != nil {
		h.FSChart.Hidden = hidden(h.FSChart.ID)
	}
	for i := range h.GuestCharts {
		h.GuestCharts[i].Hidden = hidden(h.GuestCharts[i].ID)
	}
	h.Hidden = map[string]bool{}
	for id, it := range layout {
		if it.Hidden {
			h.Hidden[id] = true
		}
	}
}

// hwHost lee y valida el host de un pedido del modo edición de Hardware.
func (s *server) hwHost(w http.ResponseWriter, r *http.Request) (hwLayoutStore, string, bool) {
	st, ok := s.prefs.(hwLayoutStore)
	if !ok {
		http.Error(w, "sin base", http.StatusNotImplemented)
		return nil, "", false
	}
	if !parseSmallForm(w, r) {
		return nil, "", false
	}
	host := r.PostFormValue("host")
	if !slices.ContainsFunc(s.poller.Snapshot().Hosts, func(h HostStatus) bool { return h.Name == host }) {
		http.Error(w, "host desconocido", http.StatusBadRequest)
		return nil, "", false
	}
	return st, host, true
}

// hwOrder guarda el orden de los mosaicos o de los gráficos de un host:
// los item del formulario, del primero al último.
func (s *server) hwOrder(w http.ResponseWriter, r *http.Request) {
	st, host, ok := s.hwHost(w, r)
	if !ok {
		return
	}
	items := r.PostForm["item"]
	if len(items) == 0 || len(items) > 50 {
		http.Error(w, "item inválido", http.StatusBadRequest)
		return
	}
	for _, it := range items {
		if !hwItem.MatchString(it) {
			http.Error(w, "item inválido", http.StatusBadRequest)
			return
		}
	}
	if err := st.SetHWOrder(r.Context(), host, items); err != nil {
		s.logger.Error("guardar el orden de Hardware", "host", host, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hwHidden oculta (hidden=1) o muestra (hidden=0) un elemento de un host.
func (s *server) hwHidden(w http.ResponseWriter, r *http.Request) {
	st, host, ok := s.hwHost(w, r)
	if !ok {
		return
	}
	item := r.PostFormValue("item")
	hidden := r.PostFormValue("hidden")
	if !hwItem.MatchString(item) || (hidden != "1" && hidden != "0") {
		http.Error(w, "item inválido", http.StatusBadRequest)
		return
	}
	if err := st.SetHWHidden(r.Context(), host, item, hidden == "1"); err != nil {
		s.logger.Error("ocultar en Hardware", "host", host, "item", item, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
