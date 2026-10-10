// Package codexcfg writes mbridge's Codex profile: ~/.codex/mbridge.config.toml,
// which `codex -p mbridge` reads, and the model catalog it names. Codex's
// config.toml belongs to magpie and the user; mbridge only takes out what
// v0.1.x once put there (SP8 spec §5.6).
package codexcfg

import (
	"regexp"
	"strings"
)

// Table is one TOML table: its name and its lines, the header first.
type Table struct {
	Name  string
	Lines []string
}

var header = regexp.MustCompile(`^\s*\[\[?\s*([A-Za-z0-9_.\-" ]+?)\s*\]\]?\s*(#.*)?$`)

// ReadTables splits a TOML file into the lines before its first table and
// its tables, each kept line for line. Only headers are recognised; values
// are never read.
func ReadTables(b []byte) (top []string, tables []Table) {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil, nil
	}
	for _, line := range strings.Split(s, "\n") {
		if m := header.FindStringSubmatch(line); m != nil {
			tables = append(tables, Table{Name: strings.ReplaceAll(m[1], " ", ""), Lines: []string{line}})
			continue
		}
		if n := len(tables); n > 0 {
			tables[n-1].Lines = append(tables[n-1].Lines, line)
		} else {
			top = append(top, line)
		}
	}
	return top, tables
}

// value is the raw value of key in lines ("" when absent), a basic string
// unquoted.
func value(lines []string, key string) string {
	for _, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' {
			if end := strings.LastIndex(v, `"`); end > 0 {
				return v[1:end]
			}
		}
		return v
	}
	return ""
}
