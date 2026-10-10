package service

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// NewDailyLog writes queqiaod.log in dir. When a write falls on a new day,
// yesterday's file becomes queqiaod-<date>.log, and only keep files are
// left in all (today's included).
func NewDailyLog(dir string, keep int) io.WriteCloser {
	return newDailyLog(dir, keep, time.Now)
}

type dailyLog struct {
	mu   sync.Mutex
	dir  string
	keep int
	now  func() time.Time
	day  string
	f    *os.File
}

func newDailyLog(dir string, keep int, now func() time.Time) *dailyLog {
	return &dailyLog{dir: dir, keep: keep, now: now}
}

func (l *dailyLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	day := l.now().Format("2006-01-02")
	if l.f == nil || day != l.day {
		if err := l.open(day); err != nil {
			return 0, err
		}
	}
	return l.f.Write(p)
}

func (l *dailyLog) open(day string) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	cur := filepath.Join(l.dir, "queqiaod.log")
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
	// the current file belongs to the day it was last written on
	if st, err := os.Stat(cur); err == nil {
		was := st.ModTime().Format("2006-01-02")
		if l.day != "" {
			was = l.day
		}
		if was != day {
			os.Rename(cur, filepath.Join(l.dir, "queqiaod-"+was+".log"))
		}
	}
	f, err := os.OpenFile(cur, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.f, l.day = f, day
	l.prune()
	return nil
}

// prune keeps the newest keep-1 dated files beside the current one.
func (l *dailyLog) prune() {
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	var dated []string
	for _, e := range ents {
		if n := e.Name(); strings.HasPrefix(n, "queqiaod-") && strings.HasSuffix(n, ".log") {
			dated = append(dated, n)
		}
	}
	sort.Strings(dated) // the date sorts as text
	for len(dated) > l.keep-1 {
		os.Remove(filepath.Join(l.dir, dated[0]))
		dated = dated[1:]
	}
}

func (l *dailyLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
