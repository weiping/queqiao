package main

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The project was called queqiao until SP9. Nothing outside the history
// keeps that name: not the docs, the scripts, the workflows nor the code.
// Kept on purpose: docs/superpowers (the history), recorded captures
// (internal/wire/testdata, internal/magpie/testdata: literal bytes), this
// file, paths into docs/superpowers, assertions marked "recorded before
// the rename", and the README line that says where the name came from.
func TestNoQueqiaoLeft(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "grep", "-n", "-i", "-I", "-E", `queqiao|/v1/queqiao|group/qq-|qq-v[0-9*]`, "--",
		".", ":!docs/superpowers", ":!internal/wire/testdata", ":!internal/magpie/testdata", ":!cmd/mbridge/name_test.go")
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return // git grep found nothing
	}
	if err != nil {
		t.Skip("not in a git checkout:", err)
	}
	history := regexp.MustCompile(`docs/superpowers/[^\s"')\]]+`)
	old := regexp.MustCompile(`(?i)queqiao|group/qq-|qq-v[0-9*]`)
	var left []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(l, "README.md:") && strings.Contains(l, "曾叫 queqiao") {
			continue
		}
		if strings.Contains(l, "recorded before the rename") {
			continue // asserts a recorded capture's bytes
		}
		// a path into the history keeps its name
		if _, text, ok := strings.Cut(l, ":"); ok && !old.MatchString(history.ReplaceAllString(text, "")) {
			continue
		}
		left = append(left, l)
	}
	if len(left) > 0 {
		t.Fatalf("%d lines still say the old name:\n%s", len(left), strings.Join(left, "\n"))
	}
}

// What the generic queqiao→mbridge replace made wrong once: the deleted
// migrate command and a marketplace called mbridge (it is magpie-bridge).
func TestNoRenameLeftovers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "grep", "-n", "-I", "-E", `mbridge migrate|@mbridge\b`, "--",
		".", ":!docs/superpowers", ":!cmd/mbridge/name_test.go")
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return
	}
	if err != nil {
		t.Skip("not in a git checkout:", err)
	}
	t.Fatalf("rename leftovers:\n%s", out)
}
