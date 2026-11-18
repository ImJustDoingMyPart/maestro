package hub

import (
	"net/http"
	"time"
)

// La portada hecha con Svelte (web/src/portada) pide sus datos en JSON a
// /api/portada: lo mismo que servicesData le da a la plantilla, con nombres
// en minúscula.

type apiPortada struct {
	Live         bool             `json:"live"`
	Demo         bool             `json:"demo"`
	Groups       []apiGroup       `json:"groups"`
	Summary      apiSummary       `json:"summary"`
	HiddenCount  int              `json:"hiddenCount"`
	FoundPending int              `json:"foundPending"`
	FoundFailing int              `json:"foundFailing"`
	HostProblems []apiHostProblem `json:"hostProblems"`
	Updated      string           `json:"updated"` // RFC 3339; vacío si no hubo vuelta
}

type apiSummary struct {
	Up       int `json:"up"`
	Degraded int `json:"degraded"`
	Down     int `json:"down"`
	Unknown  int `json:"unknown"`
	Stopped  int `json:"stopped"`
}

type apiHostProblem struct {
	Name   string `json:"name"`
	Err    string `json:"err"`
	LastOK string `json:"lastOK"` // vacío si nunca respondió
}

type apiGroup struct {
	Name        string    `json:"name"`
	Otros       bool      `json:"otros"`
	Locked      bool      `json:"locked"`
	Empty       bool      `json:"empty"`
	OrderLocked bool      `json:"orderLocked"`
	Cards       []apiCard `json:"cards"`
}

type apiLogo struct {
	Src     string `json:"src"`
	SrcDark string `json:"srcDark,omitempty"`
	Mono    bool   `json:"mono,omitempty"`
}

type apiCard struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	Icon           string      `json:"icon"`
	Logo           *apiLogo    `json:"logo"`
	IconSet        string      `json:"iconSet"`
	IconLocked     bool        `json:"iconLocked"`
	URL            string      `json:"url"`
	Href           string      `json:"href"`
	State          State       `json:"state"`
	StateLabel     string      `json:"stateLabel"`
	Reason         string      `json:"reason"`
	Group          string      `json:"group"`
	Members        []apiMember `json:"members"`
	StackHref      string      `json:"stackHref"`
	MetricsHref    string      `json:"metricsHref"`
	NameLocked     bool        `json:"nameLocked"`
	GroupLocked    bool        `json:"groupLocked"`
	Separated      bool        `json:"separated"`
	SeparateLocked bool        `json:"separateLocked"`
	EditHref       string      `json:"editHref"`
	Found          bool        `json:"found"`
	HiddenLocked   bool        `json:"hiddenLocked"`
	Alert          bool        `json:"alert"`
	AlertLocked    bool        `json:"alertLocked"`
	UpHref         string      `json:"upHref,omitempty"`
}

type apiMember struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	Href           string `json:"href"`
	NameLocked     bool   `json:"nameLocked"`
	SeparateLocked bool   `json:"separateLocked"`
	HiddenLocked   bool   `json:"hiddenLocked"`
	Alert          bool   `json:"alert"`
	AlertLocked    bool   `json:"alertLocked"`
	State          State  `json:"state"`
	StateLabel     string `json:"stateLabel"`
	Reason         string `json:"reason"`
}

// portadaJSON pasa los datos de la plantilla a los del JSON.
func portadaJSON(d pageData) apiPortada {
	out := apiPortada{
		Live:         d.Live,
		Demo:         d.Demo,
		Groups:       []apiGroup{},
		Summary:      apiSummary(d.Summary),
		HiddenCount:  d.HiddenCount,
		FoundPending: d.FoundPending,
		FoundFailing: d.FoundFailing,
		HostProblems: []apiHostProblem{},
		Updated:      rfc3339(d.Updated),
	}
	for _, h := range d.HostProblems {
		out.HostProblems = append(out.HostProblems, apiHostProblem{Name: h.Name, Err: h.Err, LastOK: rfc3339(h.LastOK)})
	}
	for _, g := range d.Groups {
		ag := apiGroup{Name: g.Name, Otros: g.Otros, Locked: g.Locked, Empty: g.Empty, OrderLocked: g.OrderLocked, Cards: []apiCard{}}
		for _, c := range g.Services {
			ag.Cards = append(ag.Cards, cardJSON(c))
		}
		out.Groups = append(out.Groups, ag)
	}
	return out
}

func cardJSON(c serviceCard) apiCard {
	ac := apiCard{
		Key: c.Key, Name: c.Name, Description: c.Description, Icon: c.Icon,
		IconSet: c.IconSet, IconLocked: c.IconLocked, URL: c.URL, Href: c.Href,
		State: c.State, StateLabel: c.State.Label(), Reason: c.Reason, Group: c.Group,
		Members: []apiMember{}, StackHref: c.StackHref, MetricsHref: c.MetricsHref,
		NameLocked: c.NameLocked, GroupLocked: c.GroupLocked, Separated: c.Separated,
		SeparateLocked: c.SeparateLocked, EditHref: c.EditHref, Found: c.Found,
		HiddenLocked: c.HiddenLocked, Alert: c.Alert, AlertLocked: c.AlertLocked,
		UpHref: c.UpHref,
	}
	if c.Logo != nil {
		ac.Logo = &apiLogo{Src: c.Logo.Src, SrcDark: c.Logo.SrcDark, Mono: c.Logo.Mono}
	}
	for _, m := range c.Members {
		ac.Members = append(ac.Members, apiMember{
			Key: m.Key, Name: m.Name, Href: m.Href, NameLocked: m.NameLocked,
			SeparateLocked: m.SeparateLocked, HiddenLocked: m.HiddenLocked,
			Alert: m.Alert, AlertLocked: m.AlertLocked,
			State: m.State, StateLabel: m.State.Label(), Reason: m.Reason,
		})
	}
	return ac
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// portadaAPI atiende GET /api/portada.
func (s *server) portadaAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, portadaJSON(s.servicesData(r.Context())))
}
