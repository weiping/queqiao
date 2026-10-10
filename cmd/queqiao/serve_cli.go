package main

import (
	"context"
	"fmt"
	"github.com/weiping/queqiao/internal/service"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/weiping/queqiao/internal/fsutil"
	"github.com/weiping/queqiao/internal/magpie"
	"github.com/weiping/queqiao/internal/proxy"
	"github.com/weiping/queqiao/internal/router"
)

// serveCmd runs queqiaod in the foreground (SP8 §5.5): the /v1/queqiao/*
// endpoints and the proxy to official magpie. A router.json that can't be
// loaded leaves it passing everything through, and says why.
func serveCmd(args []string) error {
	detach, err := serveArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stderr for a terminal, logs/queqiaod.log (a day a file, a week kept)
	// for the service, which has none
	logf := service.NewDailyLog(filepath.Join(fsutil.ConfigDir(), "logs"), 7)
	defer logf.Close()
	if detach {
		// the Windows task's console: let it go, so no window stays open
		// (closing it would end queqiaod); the log file is the output
		detachConsole()
		log.SetOutput(logf)
	} else {
		// the file first: a write MultiWriter can't make stops the rest
		log.SetOutput(io.MultiWriter(logf, os.Stderr))
	}
	r := newReloader(filepath.Join(fsutil.ConfigDir(), "router.json"))
	log.Printf("queqiaod on %s, magpie at %s", r.listen, r.target)
	return proxy.Serve(ctx, r.listen, r)
}

// reloader serves queqiaod from router.json as it is now: the service
// starts before `queqiao router init` writes it, and users edit it by
// hand. Every request looks at the file's size and time at most once a
// second and rebuilds the handler when they changed, keeping the session
// state and hints queqiaod holds. Only listen needs a restart.
type reloader struct {
	path  string
	every time.Duration

	mu      sync.Mutex
	checked time.Time
	stamp   string
	h       http.Handler
	deps    *router.Deps
	listen  string
	target  *url.URL
}

func newReloader(path string) *reloader {
	r := &reloader{path: path, every: time.Second}
	r.stamp = stampOf(path)
	r.build()
	return r
}

func (r *reloader) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	if now := time.Now(); now.Sub(r.checked) >= r.every {
		r.checked = now
		if st := stampOf(r.path); st != r.stamp {
			r.stamp = st
			r.build()
		}
	}
	h := r.h
	r.mu.Unlock()
	h.ServeHTTP(w, req)
}

// build loads router.json into a fresh handler; r.mu is held (or r is new).
func (r *reloader) build() {
	listen, target, deps := queqiaodDeps(r.path)
	if deps != nil {
		router.SetConfigError(nil)
		if r.deps != nil { // the turns in flight keep their state
			deps.Sessions, deps.Hints = r.deps.Sessions, r.deps.Hints
		}
		if r.listen != "" {
			log.Println("queqiaod: router.json reloaded")
		}
	}
	if r.listen != "" && listen != r.listen {
		log.Printf("queqiaod: listen changed to %s; it takes effect when queqiaod restarts (queqiao service install)", listen)
	} else {
		r.listen = listen
	}
	r.h, r.deps, r.target = proxy.Handler(target, deps), deps, target
}

// stampOf is a file's size and time, or "" when it can't be read.
func stampOf(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d", st.Size(), st.ModTime().UnixNano())
}

// serveArgs reads serve's one flag, --detach (the Windows task's).
func serveArgs(args []string) (detach bool, err error) {
	for _, a := range args {
		if a != "--detach" {
			return false, fmt.Errorf("serve takes only --detach (router.json's listen and magpie_url set the addresses)")
		}
		detach = true
	}
	return detach, nil
}

// queqiaodDeps loads router.json into what queqiaod runs on; deps is nil
// when it can't be loaded, with the default addresses.
func queqiaodDeps(path string) (listen string, target *url.URL, deps *router.Deps) {
	cfg, err := router.Load(path, "")
	if err != nil {
		router.SetConfigError(err)
		log.Println("queqiaod: router.json:", err, "— passing every request through to magpie")
		u, _ := url.Parse(magpie.DefaultURL)
		return "127.0.0.1:3426", u, nil
	}
	u, err := url.Parse(cfg.MagpieURL)
	if err != nil || u.Host == "" {
		u, _ = url.Parse(magpie.DefaultURL)
	}
	c := magpie.New(cfg.MagpieURL)
	return cfg.Listen, u, &router.Deps{Config: cfg, Classify: router.NewClassifier(cfg, router.AskVia(c))}
}
