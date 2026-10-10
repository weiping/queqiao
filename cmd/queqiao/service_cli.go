package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weiping/queqiao/internal/service"
)

var newService = service.New

// serviceCmd is `queqiao service install|uninstall|status` (SP8 §5.7).
func serviceCmd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: queqiao service install|uninstall|status")
	}
	ctx := context.Background()
	m := newService()
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
		if err := m.Install(ctx, exe); err != nil {
			return err
		}
		path, _ := service.Unit(m.GOOS, m.Home, m.ConfigDir, exe)
		fmt.Println(green.Render("✓"), "queqiaod runs at login:", path)
		return nil
	case "uninstall":
		if err := m.Uninstall(ctx); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "queqiaod no longer runs at login")
		return nil
	case "status":
		running, detail, err := m.Status(ctx)
		if err != nil {
			return err
		}
		mark := amber.Render("✗")
		if running {
			mark = green.Render("✓")
		}
		fmt.Println(mark, "service:", detail)
		return nil
	}
	return fmt.Errorf("usage: queqiao service install|uninstall|status")
}
