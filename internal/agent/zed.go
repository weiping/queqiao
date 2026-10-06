package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/edit"
)

const zedProvider = "language_models.openai_compatible.magpie"
const zedModel = "agent.default_model"

// Zed stores API keys in the OS credential store, keyed by the API URL.
var zedCredential = saveZedCredential

// Zed uses ~/.config on macOS, XDG on Linux and Roaming AppData on Windows.
func zed(home, cfg string) *Agent {
	bin := os.Getenv("MAGPIE_ZED_BIN")
	if bin == "" {
		bin = "zed"
	}
	processes := zedProcessNames()
	if custom := appdir.Getenv("MAGPIE_ZED_CONFIG_DIR"); custom != "" {
		return zedAtWith(custom, bin, processes)
	}
	switch runtime.GOOS {
	case "darwin":
		cfg = filepath.Join(home, ".config")
	case "windows":
		cfg = appdir.Getenv("APPDATA")
		if cfg == "" {
			cfg = filepath.Join(home, "AppData", "Roaming")
		}
		return zedAtWith(filepath.Join(cfg, "Zed"), bin, processes)
	}
	return zedAtWith(filepath.Join(cfg, "zed"), bin, processes)
}

func zedAt(dir string) *Agent {
	return zedAtWith(dir, "zed", zedProcessNames())
}

func zedAtWith(dir, bin string, processes []string) *Agent {
	path := filepath.Join(dir, "settings.json")
	get := func(k string) string { v, _ := edit.GetJSON(path, k); return v }
	model := pairGet(func(k string) (string, bool) { return edit.GetJSON(path, k) }, zedModel+".provider", zedModel+".model")
	key := "zed:" + path + ":"
	return atomic(&Agent{
		ID: "zed", Name: "Zed", Icon: "zed", Bin: bin, Dir: dir, Path: path, Spelled: prefixed,
		UA: []string{"zed"},
		Notice: func() string {
			if usesMagpie(model()) && Running(processes...) {
				return "Restart Zed if it still asks for an API key: magpie has configured its gateway credential in the system credential store."
			}
			return ""
		},
		Sync: func() error {
			if get(zedProvider+".api_url") != gatewayV1() {
				return nil
			}
			return syncJSON(path, zedProvider+".available_models", func() any {
				return zedProviderJSON()["available_models"]
			})
		},
		Check: func() string {
			if !usesMagpie(model()) {
				return ""
			}
			return wiringOff("Zed", path, func(k string) (string, bool) { return edit.GetJSON(path, zedProvider+"."+k) }, "api_url", gatewayV1())
		},
		Fields: []Field{{
			Key: "model", Label: "model", Get: model,
			Set: func(v string) error {
				if ref, ok := strings.CutPrefix(v, magpieID+"/"); ok && isMagpie(ref) {
					if err := zedCredential(gatewayV1()); err != nil {
						return fmt.Errorf("configure Zed gateway credential: %w", err)
					}
					if !usesMagpie(model()) {
						previous := map[string]string{key + "model": get(zedModel)}
						if get(zedProvider+".api_url") != gatewayV1() {
							previous[key+"provider"] = get(zedProvider)
						}
						stash(previous)
					}
					return edit.SetJSON(path,
						edit.KV{Path: zedProvider, Value: zedProviderJSON()},
						edit.KV{Path: zedModel, Value: map[string]string{"provider": magpieID, "model": ref}})
				}
				// Validate a native selection before removing magpie's wiring.
				p, m, ok := strings.Cut(v, "/")
				if v != "" && (!ok || p == "" || m == "") {
					return fmt.Errorf("expected provider/model, got %q", v)
				}
				if get(zedProvider+".api_url") == gatewayV1() || usesMagpie(model()) {
					for _, entry := range []struct{ name, field string }{{"provider", zedProvider}, {"model", zedModel}} {
						if entry.name == "model" && !usesMagpie(model()) {
							forget(key + entry.name)
							continue
						}
						if was := unstash(key + entry.name); was != "" {
							if err := edit.SetJSON(path, edit.KV{Path: entry.field, Value: json.RawMessage(was)}); err != nil {
								return err
							}
						} else if entry.name == "provider" || usesMagpie(model()) {
							if err := edit.DelJSON(path, entry.field); err != nil {
								return err
							}
						}
					}
				} else if v == "" {
					return edit.DelJSON(path, zedModel)
				}
				if v == "" {
					return nil
				}
				return edit.SetJSON(path, edit.KV{Path: zedModel, Value: map[string]string{"provider": p, "model": m}})
			},
			Options: func(cur map[string]string) []Option {
				var own []Option
				if v := cur["model"]; v != "" && !usesMagpie(v) {
					own = append(own, Option{Value: v, Icon: modelIcon("", v)})
				}
				return append(group("Zed", own), viaMagpie("zed", magpieID+"/")...)
			},
		}},
	}, path, stashPath())
}

func zedProcessNames() []string {
	value := os.Getenv("MAGPIE_ZED_PROCESS_NAMES")
	if value == "" {
		return []string{`(^|/)(zed|zeditor|zed-editor)( |$)`}
	}
	var out []string
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, `(^|/)`+regexp.QuoteMeta(name)+`( |$)`)
		}
	}
	if len(out) == 0 {
		return []string{`(^|/)(zed|zeditor|zed-editor)( |$)`}
	}
	return out
}

func zedProviderJSON() map[string]any {
	models := []any{}
	for _, m := range magpieModels("zed") {
		context := m.Context
		if context == 0 {
			context = 128000 // Zed requires a context window for every custom model.
		}
		// Zed's max_tokens is the window a prompt and its reply share: it
		// keeps max_output_tokens of it for the reply and lets the prompt
		// fill the rest before it compacts. Context is what a prompt may
		// hold, so the window is it and the reply together (#850); written
		// as Context alone, a model whose reply may be as long as its
		// prompt (glm-4.6) left Zed no room for a prompt at all.
		entry := map[string]any{
			"name": m.ID, "display_name": m.Name, "max_tokens": context,
			"capabilities": map[string]any{
				"tools": true, "images": m.Images,
				"parallel_tool_calls": false, "prompt_cache_key": false,
				"chat_completions": true,
			},
		}
		if output := m.Output; output > 0 {
			if m.Context == 0 {
				// Keep the fallback reply cap when the prompt limit is unknown.
				output = min(output, context)
			}
			entry["max_output_tokens"] = output
			entry["max_tokens"] = context + output
		}
		models = append(models, entry)
	}
	return map[string]any{"api_url": gatewayV1(), "available_models": models}
}
