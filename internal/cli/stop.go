package cli

import (
	"fmt"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// StopGrace is how long SIGTERM gets before SIGKILL.
const StopGrace = 5 * time.Second

func init() {
	Register(Command{Name: "stop", Summary: "stop a server: stop [name|port|project/name] | --project P | --worktree DIR | --all [--yes]", Run: cmdStop})
}

func cmdStop(env *Env, args []string) error {
	fs := newFlagSet(env, "stop")
	project := fs.String("project", "", "stop every server of this project")
	all := fs.Bool("all", false, "stop every server")
	worktree := fs.String("worktree", "", "stop every server in this worktree directory")
	yes := fs.Bool("yes", false, "skip the confirmation for --project/--all/--worktree")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if *project != "" || *all || *worktree != "" {
		return env.stopWide(*project, *worktree, *all, *yes, pos)
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	f, err := env.Store.Load()
	if err != nil {
		return err
	}
	e, err := env.resolveEntry(f, "stop", name)
	if err != nil {
		return err
	}
	return env.stopEntry(*e)
}

// stopEntry stops a server via its runner and drops it from the registry.
// The registry entry is kept if the stop fails so nothing is lost track of.
func (env *Env) stopEntry(e registry.Entry) error {
	rn, err := runner.Get(e.Kind)
	if err != nil {
		return err
	}
	if err := rn.Stop(e, StopGrace); err != nil {
		return fmt.Errorf("stopping %s: %w", e.Name, err)
	}
	err = env.Store.Update(func(f *registry.File) error {
		// Only remove the entry we stopped, not a newer one under the same key.
		if cur := f.Find(e.Key); cur != nil && cur.PID == e.PID && cur.PIDStart == e.PIDStart {
			f.Remove(e.Key)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stderr, "devrun: stopped %s (port %d freed)\n", e.Name, e.Port())
	return nil
}
