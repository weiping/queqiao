//go:build !race

package proxy

// The first-byte overhead (spec §3.2, 5ms at p95) is a timing: the race
// detector slows the instrumented proxy several times over (a macOS CI
// runner measured +9.6ms under -race), so it is measured without it; CI
// runs it in a step of its own.

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The proxy's added wait before the first byte of a stream, at p95 over
// loopback, stays under 5 ms (SP8 spec §3.2 item 5).
func TestProxyFirstByteOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	e := setup(t)
	e.up.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
	}
	mag := httptest.NewServer(e.up)
	defer mag.Close()
	first := func(base string, hdr map[string]string, body string) time.Duration {
		req, _ := http.NewRequest("POST", base+"/v1/responses", strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		start := time.Now()
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bufio.NewReader(res.Body).ReadString('\n')
		d := time.Since(start)
		res.Body.Close()
		return d
	}
	p95 := func(ds []time.Duration) time.Duration {
		slices := append([]time.Duration(nil), ds...)
		for i := range slices {
			for j := i + 1; j < len(slices); j++ {
				if slices[j] < slices[i] {
					slices[i], slices[j] = slices[j], slices[i]
				}
			}
		}
		return slices[len(slices)*95/100]
	}
	var direct, proxied []time.Duration
	for i := 0; i < 300; i++ {
		body := responses(fmt.Sprintf("turn %d", i), false)
		direct = append(direct, first(mag.URL, codexHdr, strings.Replace(body, "group/mbridge", "group/mb-fast", 1)))
		hdr := map[string]string{"session-id": fmt.Sprintf("s-%d", i), "User-Agent": "codex"}
		proxied = append(proxied, first(e.srv.URL, hdr, body))
	}
	d, p := p95(direct), p95(proxied)
	t.Logf("first byte p95: direct %v, through mbridge %v (+%v)", d, p, p-d)
	if p-d > 5*time.Millisecond {
		t.Fatalf("proxy adds %v at p95", p-d)
	}
}
