// Package islands holds the /islands page's compile-checked samples.
package islands

import (
	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// snippet:start cdn
// Gauge draws with ECharts from a CDN. Declaring the script is what puts the
// CDN's origin in the page's script-src; nothing else widens the policy.
type Gauge struct {
	Value via.Signal[int] `via:"init=0"`
}

func (g *Gauge) PageMeta() via.Meta {
	return via.Meta{
		Title: "Gauge",
		Assets: via.Assets{Scripts: []via.Script{
			{Src: "https://cdn.jsdelivr.net/npm/echarts@5.5.1/dist/echarts.min.js", Defer: true},
			{Src: "/static/gauge.js", Defer: true},
		}},
	}
}

func (g *Gauge) View() h.H {
	return h.Div(h.Class("gauge"), h.DataIgnoreMorph(),
		h.DataEffect(expr.Call("viaGauge", expr.El, g.Value.Ref())))
}

// snippet:end
