// Package forkhook connects the Router to the fork's gateway hook while
// queqiao is still built inside magpie (SP8 Tasks 4–8). It goes with the
// magpie tree in Task 9, when queqiaod's proxy calls the Router directly.
package forkhook

import (
	"context"
	"net/http"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
	"github.com/yetone/magpie/internal/wire"
)

// GatewayHook adapts the Router to the fork's gateway hook (SP8: queqiaod's
// proxy calls the Router directly; this goes with the magpie tree in Task
// 9). Tier-group requests are only observed; router-group requests are
// routed.
func GatewayHook(rt *router.Router, routerGroup string) gateway.RouterHookFunc {
	return func(h http.Header, body []byte, req *gateway.Request, g provider.Group, _ []provider.Member, agent string) *gateway.RuleHit {
		wr := toWire(req)
		if g.ID != routerGroup {
			rt.Observe(h, wr)
			return nil
		}
		r, ok := rt.Route(context.Background(), h, body, wr, "gateway")
		if !ok {
			return nil
		}
		return &gateway.RuleHit{N: 1, Use: r.Group, Router: &gateway.RouterHit{
			Tier: string(r.Tier), Reason: r.Reason, Source: r.Source, Hint: r.Hint, Arm: r.Arm}}
	}
}

// toWire is the fork gateway's parsed request as wire reads one.
func toWire(req *gateway.Request) *wire.Request {
	if req == nil {
		return nil
	}
	out := &wire.Request{Model: req.Model}
	for _, m := range req.Messages {
		wm := wire.Message{Role: m.Role}
		for _, p := range m.Parts {
			k := wire.Other
			switch p.Kind {
			case gateway.Text:
				k = wire.Text
			case gateway.Image, gateway.File:
				k = wire.Media
			case gateway.ToolCall:
				k = wire.ToolCall
			case gateway.ToolResult:
				k = wire.ToolResult
			case gateway.Thinking:
				k = wire.Thinking
			}
			wm.Parts = append(wm.Parts, wire.Part{Kind: k, Text: p.Text, IsError: p.IsError})
		}
		out.Messages = append(out.Messages, wm)
	}
	return out
}
