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
	HWHosts(ctx context.Context) ([]string, error)
	SetHWHosts(ctx context.Context, hosts []string) error
}

// hwItem es la clave de un elemento de Hardware: "tile:cpu", "chart:mem",
// "section:storage", "fs:/mnt/data", "disk:/dev/sda", "guest:102" (ver
// itemID).
var hwItem = regexp.MustCompile(`^(tile|chart|section|fs|disk|guest):[A-Za-z0-9._/@-]{1,100}$`)

// applyHWLayout ordena las secciones, las tarjetas y los gráficos de un
// host y marca lo oculto. Lo que tiene posición va primero, en ese orden;
// lo demás después, en el orden de siempre.
func applyHWLayout(h *hostHardware, layout map[string]store.HWItem) {
	pos := func(id string) int {
		if it, ok := layout[id]; ok && it.Position >= 0 {
			return it.Position
		}
		return 1 << 30
	}
	hidden := func(id string) bool { return layout[id].Hidden }
	cards := func(cs []hwCard) {
		slices.SortStableFunc(cs, func(a, b hwCard) int { return cmp.Compare(pos(a.ID), pos(b.ID)) })
		for i := range cs {
			cs[i].Hidden = hidden(cs[i].ID)
		}
	}
	charts := func(cs []chartSpec) {
		slices.SortStableFunc(cs, func(a, b chartSpec) int { return cmp.Compare(pos(a.ID), pos(b.ID)) })
		for i := range cs {
			cs[i].Hidden = hidden(cs[i].ID)
		}
	}
	cards(h.Tiles)
	cards(h.Storage)
	cards(h.GuestCards)
	charts(h.Charts)
	charts(h.GuestCharts)
	if h.FSChart != nil {
		h.FSChart.Hidden = hidden(h.FSChart.ID)
	}
	slices.SortStableFunc(h.Sections, func(a, b hwSection) int { return cmp.Compare(pos(a.ID), pos(b.ID)) })
	for i := range h.Sections {
		h.Sections[i].Hidden = hidden(h.Sections[i].ID)
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

// hwHosts atiende POST /hardware/hosts: el orden de las pestañas de los
// hosts (los host del formulario, del primero al último). El primero es el
// que se abre al entrar.
func (s *server) hwHosts(w http.ResponseWriter, r *http.Request) {
	st, ok := s.prefs.(hwLayoutStore)
	if !ok {
		http.Error(w, "sin base", http.StatusNotImplemented)
		return
	}
	if !parseSmallForm(w, r) {
		return
	}
	hosts := r.PostForm["host"]
	known := s.poller.Snapshot().Hosts
	if len(hosts) == 0 || len(hosts) > len(known) {
		http.Error(w, "host inválido", http.StatusBadRequest)
		return
	}
	for i, h := range hosts {
		if !slices.ContainsFunc(known, func(k HostStatus) bool { return k.Name == h }) || slices.Contains(hosts[:i], h) {
			http.Error(w, "host inválido", http.StatusBadRequest)
			return
		}
	}
	if err := st.SetHWHosts(r.Context(), hosts); err != nil {
		s.logger.Error("guardar el orden de los hosts", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
