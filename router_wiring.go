package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
)

// wireRouter loads ~/.config/queqiao/router.json and, when it is valid,
// installs the router's gateway hook and its /v1/queqiao endpoints. A
// missing or invalid config degrades: the gateway serves exactly as plain
// magpie and `queqiao router status` reports why (spec §6.2). The wiring
// lives here — in package main — so the gateway never imports the router
// package (router imports gateway for its types; the other direction
// would be a cycle).
func wireRouter(s *gateway.Server) {
	cfg, err := router.Load(filepath.Join(appdir.Config(), "router.json"), "")
	if err != nil {
		router.SetConfigError(err)
		return
	}
	deps := &router.Deps{Config: cfg, Classify: router.NewClassifier(cfg, askRouter(s))}
	managed := map[string]bool{cfg.RouterGroup: true}
	for _, tc := range cfg.Tiers {
		managed[tc.Group] = true
	}
	gateway.SetRouterHook(router.NewHook(deps).GatewayHookFunc, func(g provider.Group) bool {
		return managed[g.ID]
	})
	gateway.MuxRegister = append(gateway.MuxRegister, func(mux *http.ServeMux) {
		router.Register(mux, deps)
	})
}

// askRouter adapts the gateway's own model paths to the classifier's ask
// callback: a typesafe/* decider goes to its System One provider, any
// other model through the gateway itself.
func askRouter(s *gateway.Server) func(ctx context.Context, model, body string) (string, error) {
	return func(ctx context.Context, model, body string) (string, error) {
		if strings.HasPrefix(model, "typesafe/") {
			b, err := s.AskDecider(ctx, model, body)
			return string(b), err
		}
		return s.AskChat(ctx, model, body)
	}
}
