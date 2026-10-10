package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// Server states shown by ls.
const (
	StateStarting = "starting"
	StateUp       = "up"
	StateDead     = "dead"
)

// DefaultName is the server name used when --name is omitted on `up`.
const DefaultName = "web"

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(s string) string { return strings.Trim(slugRe.ReplaceAllString(s, "-"), "-") }

// logPath is the log file for a server key.
func (env *Env) logPath(project, worktree, name string) string {
	return filepath.Join(env.Store.LogDir(), fmt.Sprintf("%s-%s-%s.log",
		slug(project), slug(filepath.Base(worktree)), slug(name)))
}

// stateOf classifies a server. listening may be nil, in which case a live
// server counts as starting.
func stateOf(e *registry.Entry, listening map[int]bool) string {
	r, err := runner.Get(e.Kind)
	if err != nil || !r.Alive(*e) {
		return StateDead
	}
	for _, p := range e.Ports {
		if !listening[p] {
			return StateStarting
		}
	}
	return StateUp
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
