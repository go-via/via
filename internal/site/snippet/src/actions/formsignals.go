package actions

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

var regions = map[string][]string{"pt": {"Lisboa", "Porto"}, "br": {"Bahia", "Pará"}}

// snippet:start formsignals
type Profile struct {
	Name    via.Signal[string]
	Country via.Signal[string] `via:"init=\"pt\""`
	Region  via.Signal[string]
	err     string
}

func (p *Profile) CountryChanged(ctx *via.Ctx) { p.Region.Set("") }

func (p *Profile) Save(ctx *via.Ctx) {
	r := ctx.Request()
	p.Name.Set(strings.TrimSpace(r.FormValue("name")))
	p.Country.Set(r.FormValue("country"))
	p.Region.Set(r.FormValue("region"))
	if p.Name.Get() == "" {
		p.err = "Name is required."
	}
}

func (p *Profile) View() h.H {
	return via.PostForm(p.Save,
		h.Input(h.Name("name"), p.Name.Bind()),
		h.Select(h.Name("country"), p.Country.Bind(), on.Change(p.CountryChanged),
			h.Option(h.Value("pt"), h.Str("Portugal")),
			h.Option(h.Value("br"), h.Str("Brasil"))),
		h.Select(h.Name("region"), p.Region.Bind(), via.Each(regions[p.Country.Get()], option)),
		h.Small(h.Str(p.err)),
		h.Button(h.Type("submit"), h.Str("Save")),
	)
}

// snippet:end

func option(v string) h.H { return h.Option(h.Value(v), h.Str(v)) }
