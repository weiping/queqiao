package codexcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/weiping/queqiao/internal/fsutil"
)

// Models are the models queqiao's Codex profile offers: the router group
// first, then the three tiers.
var Models = []string{"group/queqiao", "group/qq-fast", "group/qq-balanced", "group/qq-perf"}

// Init writes queqiao.config.toml and queqiao-models.json into codexHome,
// queqiaoURL being queqiaod's /v1, after CleanLegacy. changed lists the
// files it wrote (none when everything was already so).
func Init(codexHome, queqiaoURL string) (changed []string, err error) {
	cleaned, err := CleanLegacy(codexHome)
	if err != nil {
		return nil, err
	}
	if cleaned {
		changed = append(changed, filepath.Join(codexHome, "config.toml"))
	}
	bearer := ""
	if b, err := os.ReadFile(filepath.Join(codexHome, "config.toml")); err == nil {
		_, tables := ReadTables(b)
		for _, t := range tables {
			if t.Name == "model_providers.magpie" {
				bearer = value(t.Lines, "experimental_bearer_token")
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	catalog := filepath.Join(codexHome, "queqiao-models.json")
	var p bytes.Buffer
	p.WriteString("# queqiao's Codex profile: codex -p queqiao (written by `queqiao router init`)\n")
	p.WriteString("model_provider = \"queqiao\"\n")
	p.WriteString("model = " + quote("group/queqiao") + "\n")
	p.WriteString("model_catalog_json = " + quote(catalog) + "\n\n")
	p.WriteString("[model_providers.queqiao]\n")
	p.WriteString("name = \"queqiao\"\n")
	p.WriteString("base_url = " + quote(queqiaoURL) + "\n")
	p.WriteString("wire_api = \"responses\"\n")
	if bearer != "" {
		p.WriteString("experimental_bearer_token = " + quote(bearer) + "\n")
	}
	p.WriteString("http_headers = { \"x-openai-actor-authorization\" = \"magpie\" }\n")
	for _, f := range []struct {
		path string
		b    []byte
	}{
		{filepath.Join(codexHome, "queqiao.config.toml"), p.Bytes()},
		{catalog, Catalog(Models)},
	} {
		if old, err := os.ReadFile(f.path); err == nil && bytes.Equal(old, f.b) {
			continue
		}
		if err := fsutil.WriteAtomic(f.path, f.b); err != nil {
			return changed, err
		}
		changed = append(changed, f.path)
	}
	return changed, nil
}

// CleanLegacy takes out of codexHome/config.toml what qq-v0.1.x wrote
// there: [profiles.queqiao] and [model_providers.queqiao] (Codex refuses
// `-p queqiao` while a [profiles.queqiao] is in config.toml), a top-level
// model = "group/queqiao" and a model_catalog_json naming
// queqiao-models.json. The file before is kept as config.toml.queqiao-bak.
func CleanLegacy(codexHome string) (bool, error) {
	path := filepath.Join(codexHome, "config.toml")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	top, tables := ReadTables(b)
	changed := false
	var keepTop []string
	for _, l := range top {
		k, _, _ := strings.Cut(l, "=")
		switch strings.TrimSpace(k) {
		case "model":
			if value([]string{l}, "model") == "group/queqiao" {
				changed = true
				continue
			}
		case "model_catalog_json":
			if strings.HasSuffix(value([]string{l}, "model_catalog_json"), "queqiao-models.json") {
				changed = true
				continue
			}
		}
		keepTop = append(keepTop, l)
	}
	var keep []Table
	for _, t := range tables {
		if t.Name == "profiles.queqiao" || t.Name == "model_providers.queqiao" || strings.HasPrefix(t.Name, "model_providers.queqiao.") {
			changed = true
			continue
		}
		keep = append(keep, t)
	}
	if !changed {
		return false, nil
	}
	if err := fsutil.WriteAtomic(path+".queqiao-bak", b); err != nil {
		return false, err
	}
	return true, fsutil.WriteAtomic(path, join(keepTop, keep))
}

// quote is a TOML basic string.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSpace(b.String())
}
