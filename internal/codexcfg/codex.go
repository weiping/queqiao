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
// queqiaoURL being queqiaod's /v1. config.toml is only read (for magpie's
// bearer token). changed lists the files it wrote (none when everything
// was already so).
func Init(codexHome, queqiaoURL string) (changed []string, err error) {
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

// quote is a TOML basic string.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSpace(b.String())
}
