package main

import (
	"context"
	"fmt"
	"github.com/weiping/queqiao/internal/service"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/weiping/queqiao/internal/fsutil"
	"github.com/weiping/queqiao/internal/magpie"
	"github.com/weiping/queqiao/internal/proxy"
	"github.com/weiping/queqiao/internal/router"
)

// serveCmd runs queqiaod in the foreground (SP8 §5.5): the /v1/queqiao/*
// endpoints and the proxy to official magpie. A router.json that can't be
// loaded leaves it passing everything through, and says why.
func serveCmd(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("serve takes no arguments (router.json's listen and magpie_url set the addresses)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stderr for a terminal, logs/queqiaod.log (a day a file, a week kept)
	// for the service, which has none
	logf := service.NewDailyLog(filepath.Join(fsutil.ConfigDir(), "logs"), 7)
	defer logf.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logf))
	listen, target, deps := queqiaodDeps(filepath.Join(fsutil.ConfigDir(), "router.json"))
	log.Printf("queqiaod on %s, magpie at %s", listen, target)
	return proxy.Serve(ctx, listen, proxy.Handler(target, deps))
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
