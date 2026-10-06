package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
)

// resetRouterWiring undoes wireRouter's process-wide effects, so each case
// starts from a gateway that knows nothing of the router.
func resetRouterWiring(t *testing.T) {
	t.Helper()
	reset := func() {
		gateway.SetRouterHook(nil, nil)
		gateway.MuxRegister = nil
		routerWired.Store(false)
	}
	reset()
	t.Cleanup(reset)
}

// Every command that can serve the gateway itself (the window, the tray,
// `queqiao web`, `queqiao tui`, a bare `queqiao`) wires the router first:
// a gateway those start answers /v1/queqiao/*, as `queqiao serve`'s does.
// Before, only serve wired it, so a gateway the web page or the TUI served
// answered 404 there and every plugin fell back to no routing.
func TestGatewayCommandsWireTheRouter(t *testing.T) {
	for _, args := range [][]string{nil, {"web"}, {"web", "--no-open"}, {"tui"}, {"app"}, {"gui"}, {"tray"}, {"-Embedding"}, {"magpie://import?x=1"}} {
		routerHome(t)
		resetRouterWiring(t)
		if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
			t.Fatal(err)
		}
		beforeCommand(args)
		rec := httptest.NewRecorder()
		gateway.New().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/queqiao/router", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: GET /v1/queqiao/router on the gateway it serves = %d, want 200", args, rec.Code)
		}
	}
}

// Commands that never serve the gateway leave it alone: `queqiao serve`
// wires its own server, and a plain CLI command has no gateway at all.
func TestOtherCommandsLeaveTheGatewayAlone(t *testing.T) {
	for _, args := range [][]string{{"serve"}, {"ls"}, {"router", "status"}, {"claude", "group/queqiao"}} {
		routerHome(t)
		resetRouterWiring(t)
		if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
			t.Fatal(err)
		}
		beforeCommand(args)
		if len(gateway.MuxRegister) != 0 {
			t.Fatalf("%q wired the router", args)
		}
	}
}

// Wiring twice (serve after a command already wired it, or a gateway
// restarted in one process) registers the endpoints once: a second
// registration of the same pattern would panic the mux.
func TestWireRouterOnce(t *testing.T) {
	routerHome(t)
	resetRouterWiring(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	wireRouter(gateway.New())
	wireRouter(gateway.New())
	rec := httptest.NewRecorder()
	gateway.New().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/queqiao/router", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/queqiao/router = %d", rec.Code)
	}
}
