package appdir

import (
	"fmt"
	"sync"
)

var (
	nameMu    sync.Mutex
	name      = "magpie"
	nameFixed bool // a folder has been handed out; SetName must refuse from here on
)

// SetName sets the folder name magpie keeps its own files in ("magpie" by
// default). It must run before anything asks for a folder — before Config,
// Cache or SystemCache first returns a path — because files would otherwise
// be split across two folders; calling it afterwards panics.
func SetName(n string) {
	nameMu.Lock()
	defer nameMu.Unlock()
	if nameFixed {
		panic(fmt.Sprintf("appdir: SetName(%q) after a folder was handed out as %q", n, name))
	}
	name = n
}

// appName is the folder name, and marks it handed out: after the first
// call, SetName panics.
func appName() string {
	nameMu.Lock()
	defer nameMu.Unlock()
	nameFixed = true
	return name
}
