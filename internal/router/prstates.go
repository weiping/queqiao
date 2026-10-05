package router

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/proc"
	"time"
)

/**
 * PR terminal states for the report (§9): ask gh, fall back to the
 * session's own pr_merged feedback (handled in Aggregate), else unknown.
 * One query per distinct URL, cached, each bounded — a slow or missing
 * gh never blocks the report.
 */

// PRStates resolves every pr_created URL the events hold, via
// `gh pr view <url> --json state`.
func PRStates(events []Event, gh func(ctx context.Context, url string) (string, error)) map[string]string {
	out := map[string]string{}
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Kind != "feedback" || !strings.HasPrefix(ev.Extra, "pr_created") {
			continue
		}
		url := strings.TrimSpace(strings.TrimPrefix(ev.Extra, "pr_created"))
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		state, err := gh(ctx, url)
		cancel()
		if err != nil {
			out[url] = "UNKNOWN"
			continue
		}
		out[url] = strings.ToUpper(strings.TrimSpace(state))
	}
	return out
}

// GhPRState shells out to gh; "" error means gh is absent.
var GhPRState = func(ctx context.Context, url string) (string, error) {
	cmd := proc.CommandContext(ctx, "gh", "pr", "view", url, "--json", "state")
	out, err := cmd.Output()
	if err != nil {
		// gh missing (exec.Error) vs a failed query: both unknown here
		return "", err
	}
	// {"state":"MERGED"}
	line := strings.TrimSpace(string(out))
	if i := strings.Index(line, "\"state\""); i >= 0 {
		rest := line[i+len("\"state\""):]
		if j := strings.Index(rest, "\""); j >= 0 {
			rest = rest[j+1:]
			if k := strings.Index(rest, "\""); k >= 0 {
				return rest[:k], nil
			}
		}
	}
	return "", fmt.Errorf("gh: no state in %q", line)
}

// ParallelPRStates is PRStates with the gh calls fanned out (still one
// per URL; the cache map guards writes).
func ParallelPRStates(events []Event, gh func(ctx context.Context, url string) (string, error), parallel int) map[string]string {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		urls []string
		seen = map[string]bool{}
		out  = map[string]string{}
		sem  = make(chan struct{}, parallel)
	)
	for _, ev := range events {
		if ev.Kind != "feedback" || !strings.HasPrefix(ev.Extra, "pr_created") {
			continue
		}
		url := strings.TrimSpace(strings.TrimPrefix(ev.Extra, "pr_created"))
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
	}
	for _, url := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			state, err := gh(ctx, url)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[url] = "UNKNOWN"
				return
			}
			out[url] = strings.ToUpper(strings.TrimSpace(state))
		}(url)
	}
	wg.Wait()
	return out
}
