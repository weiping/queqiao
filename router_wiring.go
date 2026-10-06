package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"

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
	if routerWired.Load() {
		return
	}
	cfg, err := router.Load(filepath.Join(appdir.Config(), "router.json"), "")
	if err != nil {
		router.SetConfigError(err)
		return
	}
	if !routerWired.CompareAndSwap(false, true) {
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

// routerWired is set once wireRouter has installed the router, so a second
// call (serve after a command wired it already) does not register its
// endpoints twice.
var routerWired atomic.Bool

// beforeCommand runs ahead of every command: one that can serve the
// gateway itself wires the router first, so the gateway it starts answers
// /v1/queqiao/* and routes the tier groups exactly as `queqiao serve`'s
// does. Serve wires its own server; plain CLI commands have no gateway.
func beforeCommand(args []string) {
	if servesGateway(args) {
		// the classifier's model calls go through a gateway of their own:
		// the one the window or the TUI serves is started later, and may be
		// replaced while the process runs (a take-over, a restart)
		wireRouter(gateway.New())
	}
}

// servesGateway reports whether a command can start the gateway in this
// process: the app (bare, app, gui, tray, a magpie:// link, a Windows
// notification's -Embedding), `queqiao web` and `queqiao tui`.
func servesGateway(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if strings.HasPrefix(strings.ToLower(args[0]), "magpie:") {
		return true
	}
	switch args[0] {
	case "tui", "web", "app", "gui", "tray", "-Embedding":
		return true
	}
	return false
}
