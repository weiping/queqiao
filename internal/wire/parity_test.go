package wire_test

// Parity with the fork's gateway (SP8 Task 2): every fixture is parsed by
// both, and what the router reads from a request must agree. This file
// goes away with the magpie tree (Task 9); wire_test.go stays.

import (
	"net/http"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/wire"
)

func gatewayTools(r *gateway.Request) wire.ToolStats {
	var st wire.ToolStats
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			if p.Kind == gateway.ToolResult {
				st.Calls++
				if p.IsError {
					st.Failures++
				}
			}
		}
	}
	return st
}

func TestParityWithGateway(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range f.Headers {
				h.Set(k, v)
			}
			if got, want := wire.SessionOf(h), gateway.SessionOf(h); got != want {
				t.Errorf("SessionOf = %q, gateway %q", got, want)
			}
			p, ok := wire.ProtocolOf(f.Path)
			if !ok {
				t.Fatalf("no protocol for %s", f.Path)
			}
			wr, werr := wire.Parse(p, f.body)
			gr, gerr := gateway.ParseAt(f.Path, f.body)
			if (werr != nil) != (gerr != nil) {
				t.Fatalf("errors differ: wire %v, gateway %v", werr, gerr)
			}
			if werr != nil {
				return
			}
			if got, want := wire.UserText(wr), gateway.UserText(gr); got != want {
				t.Errorf("UserText = %q\ngateway   %q", got, want)
			}
			wt, ww := wire.TurnOf(wr)
			gt, gw := gateway.TurnOf(gr)
			if wt != gt || ww != gw {
				t.Errorf("TurnOf = (%d,%v), gateway (%d,%v)", wt, ww, gt, gw)
			}
			if got, want := wire.FirstWords(wr), gateway.FirstWords(gr); got != want {
				t.Errorf("FirstWords = %q, gateway %q", got, want)
			}
			if got, want := wire.Tools(wr), gatewayTools(gr); got != want {
				t.Errorf("Tools = %+v, gateway %+v", got, want)
			}
			if wr.Model != gr.Model {
				t.Errorf("Model = %q, gateway %q", wr.Model, gr.Model)
			}
		})
	}
}
