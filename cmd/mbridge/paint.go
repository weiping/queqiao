package main

import "os"

// paint colours a mark or a note when stdout is a terminal, and leaves
// the text as it is otherwise (pipes, tests).
type paint string

func (p paint) Render(s string) string {
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 || os.Getenv("NO_COLOR") != "" {
		return s
	}
	return "\x1b[" + string(p) + "m" + s + "\x1b[0m"
}

var (
	green = paint("32")
	amber = paint("33")
	muted = paint("90")
	faint = paint("2")
	bold  = paint("1")
)
