package main

import (
	"context"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// releasesAPI is GitHub's API (tests point it at a fake).
var releasesAPI = "https://api.github.com"

// assetName is the release asset for a platform.
func assetName(goos, goarch string) string {
	n := "queqiao-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// updateCmd is `queqiao update`: the newest qq-v release's binary for this
// platform, checked against its checksums.txt, in place of this one.
func updateCmd(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("update takes no arguments")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	return updateFrom(releasesAPI, exe)
}

func updateFrom(api, exe string) error {
	c := &http.Client{Timeout: 5 * time.Minute}
	get := func(url string) ([]byte, error) {
		res, err := c.Get(url)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("%s: %s", url, res.Status)
		}
		return io.ReadAll(res.Body)
	}
	b, err := get(api + "/repos/weiping/queqiao/releases")
	if err != nil {
		return err
	}
	var rels []struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &rels); err != nil {
		return fmt.Errorf("releases: %v", err)
	}
	name := assetName(runtime.GOOS, runtime.GOARCH)
	for _, r := range rels {
		if !strings.HasPrefix(r.Tag, "qq-v") {
			continue
		}
		var binURL, sumURL string
		for _, a := range r.Assets {
			switch a.Name {
			case name:
				binURL = a.URL
			case "checksums.txt":
				sumURL = a.URL
			}
		}
		if binURL == "" || sumURL == "" {
			return fmt.Errorf("%s has no %s or checksums.txt", r.Tag, name)
		}
		sums, err := get(sumURL)
		if err != nil {
			return err
		}
		want := ""
		sc := bufio.NewScanner(bytes.NewReader(sums))
		for sc.Scan() {
			if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
				want = f[0]
			}
		}
		bin, err := get(binURL)
		if err != nil {
			return err
		}
		got := sha256.Sum256(bin)
		if want == "" || hex.EncodeToString(got[:]) != want {
			return fmt.Errorf("%s: checksum doesn't match checksums.txt; not installed", name)
		}
		tmp := exe + ".new"
		if err := os.WriteFile(tmp, bin, 0o755); err != nil {
			return err
		}
		if runtime.GOOS == "windows" { // a running .exe can be renamed, not overwritten
			os.Remove(exe + ".old")
			if err := os.Rename(exe, exe+".old"); err != nil {
				return err
			}
		}
		if err := os.Rename(tmp, exe); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "queqiao", r.Tag, "installed at", exe)
		// the running queqiaod is still the old binary
		if err := newService().Restart(context.Background()); err != nil {
			fmt.Println(amber.Render("!"), "restart queqiaod yourself (queqiao service install):", err)
		}
		return nil
	}
	return fmt.Errorf("no qq-v release found")
}
