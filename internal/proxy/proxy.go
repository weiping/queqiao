// Package proxy is queqiaod's front door: the /v1/queqiao/* endpoints and
// a reverse proxy to official magpie that routes the router group
// (group/queqiao) per turn, for Codex and agents in gateway mode (SP8
// spec §5.5). Every other request passes through as it came.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/weiping/queqiao/internal/router"
	"github.com/weiping/queqiao/internal/wire"
)

// MaxBody is the largest request body the proxy reads to route; a larger
// one passes through unrouted (magpie's own limit).
const MaxBody = 16 << 20

// Handler serves queqiaod: the router's endpoints and the proxy to magpie
// at target. deps nil means router.json could not be loaded: everything
// passes through, and /v1/queqiao/router says why.
func Handler(target *url.URL, deps *router.Deps) http.Handler {
	mux := http.NewServeMux()
	var rt *router.Router
	if deps != nil {
		rt = router.NewRouter(deps)
		router.Register(mux, deps)
	} else {
		mux.HandleFunc("GET /v1/queqiao/router", func(w http.ResponseWriter, r *http.Request) {
			msg := "no router.json"
			if err := router.ConfigError(); err != nil {
				msg = err.Error()
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"valid": false, "error": msg})
		})
	}
	group := "queqiao"
	if deps != nil && deps.Config.RouterGroup != "" {
		group = deps.Config.RouterGroup
	}
	mux.Handle("/", New(target, rt, group))
	return mux
}

// New is the reverse proxy to magpie at target; rt nil routes nothing.
func New(target *url.URL, rt *router.Router, routerGroup string) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite:       func(pr *httputil.ProxyRequest) { pr.SetURL(target) },
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			msg := fmt.Sprintf("queqiaod can't reach magpie at %s: %v", target.Host, err)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": "proxy_error"}})
		},
	}
	return &proxy{rp: rp, rt: rt, group: "group/" + routerGroup}
}

type proxy struct {
	rp    *httputil.ReverseProxy
	rt    *router.Router
	group string
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proto, routed := wire.ProtocolOf(r.URL.Path)
	if p.rt == nil || r.Method != http.MethodPost || !routed {
		p.rp.ServeHTTP(w, r)
		return
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		http.Error(w, "reading the request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(head) > MaxBody {
		// too big to route: the rest of it follows what was read, untouched
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
		p.rt.Note(router.Event{Kind: "proxy_passthrough", Session: wire.SessionOf(r.Header), Extra: "body over 16 MiB"})
		p.rp.ServeHTTP(w, r)
		return
	}
	body := head
	if model, _, _, ok := topModel(head); ok && model == p.group {
		if req, err := wire.Parse(proto, bytes.TrimPrefix(head, bom)); err == nil {
			if rt, ok := p.rt.Route(r.Context(), r.Header, head, req, harnessOf(r.Header)); ok {
				body, _ = RewriteModel(head, rt.Group)
			}
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Content-Length")
	p.rp.ServeHTTP(w, r)
}

// harnessOf tells Codex (its User-Agent or originator says codex_…) from
// any other agent in gateway mode.
func harnessOf(h http.Header) string {
	if strings.HasPrefix(strings.ToLower(h.Get("User-Agent")), "codex") || strings.HasPrefix(strings.ToLower(h.Get("originator")), "codex") {
		return "codex"
	}
	return "gateway"
}

// loopback: listen's host is this computer's own (127.0.0.0/8, ::1,
// localhost); an empty host means every interface.
func loopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve serves h on listen until ctx ends. A port already in use is an
// error that names it and the router.json field that sets it.
func Serve(ctx context.Context, listen string, h http.Handler) error {
	if !loopback(listen) {
		return fmt.Errorf("queqiaod listens on this computer only (a loopback address such as 127.0.0.1:3426), not %s: magpie asks no key of what comes from loopback, so this would open its subscriptions to the network (router.json \"listen\")", listen)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("queqiaod can't listen on %s (router.json \"listen\" sets the address): %w", listen, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
