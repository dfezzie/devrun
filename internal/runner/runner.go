// Package runner defines how devrun starts, probes, stops and tails a server of
// a given kind. Kinds register themselves from init functions, so adding one
// (docker, ...) means adding a package and a blank import, never editing
// another kind's code.
package runner

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
)

// Spec is everything a Runner needs to start a server.
type Spec struct {
	Entry   registry.Entry // identity, cmd, ports, log path already filled in by the CLI
	Dir     string         // working directory (the worktree)
	Primary string         // name in Entry.Ports whose value substitutes {port}
}

// Handle is what a started server is known by; the CLI copies it into the
// registry entry.
type Handle struct {
	PID            int
	PGID           int
	PIDStart       uint64
	ComposeProject string
}

// LogOpts controls Logs.
type LogOpts struct {
	Follow bool
	Lines  int // trailing lines to show first (<=0 means all)
}

// Runner manages one kind of server.
type Runner interface {
	Kind() string
	// Start launches the server detached and returns immediately.
	Start(spec Spec) (Handle, error)
	// Alive reports whether the server's handle is still live. It must be cheap
	// and must never match a recycled PID.
	Alive(e registry.Entry) bool
	// Stop terminates the server, escalating after grace.
	Stop(e registry.Entry, grace time.Duration) error
	// Logs writes the server's log to out.
	Logs(ctx context.Context, e registry.Entry, opts LogOpts, out io.Writer) error
}

var (
	mu      sync.RWMutex
	runners = map[string]Runner{}
)

// Register makes r available under r.Kind(). Call from init.
func Register(r Runner) {
	mu.Lock()
	defer mu.Unlock()
	runners[r.Kind()] = r
}

// Get returns the Runner for kind.
func Get(kind string) (Runner, error) {
	mu.RLock()
	defer mu.RUnlock()
	if r, ok := runners[kind]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("no runner for kind %q (have %v)", kind, kindsLocked())
}

// Kinds lists registered kinds, sorted.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	return kindsLocked()
}

func kindsLocked() []string {
	var ks []string
	for k := range runners {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
