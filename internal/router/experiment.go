package router

import (
	"crypto/sha256"
	"encoding/binary"
)

// Arm assigns a session to the experiment arm it keeps for its whole life:
// a stable hash of the session and the config's salt, split by
// router_percent. A disabled experiment always routes.
func Arm(session string, e ExperimentConfig) string {
	if !e.Enabled {
		return "router"
	}
	sum := sha256.Sum256([]byte(e.Salt + "\x00" + session))
	v := binary.BigEndian.Uint32(sum[:4]) % 100
	if int(v) < e.RouterPercent {
		return "router"
	}
	return "control"
}
