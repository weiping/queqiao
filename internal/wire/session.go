package wire

import (
	"net/http"
	"strings"
)

// SessionHeader is the header a client names its session in for magpie.
const SessionHeader = "X-Magpie-Session"

// SessionHeaders are the headers agents name their session in, after
// SessionHeader (magpie: gateway/gateway.go sessionHeaders).
var SessionHeaders = []string{
	"x-opencode-session", "x-session-affinity", "x-session-id",
	"session_id", "session-id", "x-claude-code-session-id",
}

// SessionOf is the request's session: the first of SessionHeader and
// SessionHeaders it carries, at most 128 bytes (magpie: gateway.sessionOf).
func SessionOf(h http.Header) string {
	for _, k := range append([]string{SessionHeader}, SessionHeaders...) {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			if len(v) > 128 {
				v = v[:128]
			}
			return v
		}
	}
	return ""
}
