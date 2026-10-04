package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs f with os.Stdout redirected to a pipe and returns what
// f printed, along with f's error.
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fErr := f()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), fErr
}

func TestUpdateSaysReleases(t *testing.T) {
	groupsHome(t)
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	for _, args := range [][]string{{"update"}, {"update", "check"}} {
		out, err := captureStdout(t, func() error { return updateCmd(args) })
		if err != nil || !strings.Contains(out, "https://github.com/weiping/queqiao/releases") {
			t.Fatalf("%v: out %q err %v", args, out, err)
		}
	}
}
