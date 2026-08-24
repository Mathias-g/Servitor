package varlock

import (
	"os"
	"strings"
)

// environMap converts os.Environ() into a map. Helper kept separate so the
// package main file stays focused on the launch logic.
func environMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
