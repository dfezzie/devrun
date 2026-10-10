package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/exterkamp/devrun/internal/registry"
)

// stopStale stops (via each entry's runner) every server whose worktree
// directory no longer exists, and returns how many it stopped. Only a definite
// "does not exist" counts: an unreadable path (unmounted drive, permissions)
// is left alone. Failures are reported, not fatal, so ls and gc keep working.
func (env *Env) stopStale() int {
	f, err := env.Store.Load()
	if err != nil {
		return 0
	}
	var stale []registry.Entry
	for _, e := range f.Entries {
		if e.Worktree == "" {
			continue
		}
		if _, err := os.Stat(e.Worktree); errors.Is(err, os.ErrNotExist) {
			stale = append(stale, *e)
		}
	}
	n := 0
	for _, e := range stale {
		fmt.Fprintf(env.Stderr, "devrun: worktree %s is gone; stopping %s\n", e.Worktree, e.Name)
		if err := env.stopEntry(e); err != nil {
			fmt.Fprintln(env.Stderr, "devrun: warning:", err)
			continue
		}
		n++
	}
	return n
}
