package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"syscall"

	"github.com/exterkamp/devrun/internal/hygiene"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

func init() {
	Register(Command{Name: "gc", Summary: "drop dead entries, report orphans: gc [--kill-orphans] [--yes] [--json]", Run: cmdGc})
}

// GcReport is the stable --json shape of gc.
type GcReport struct {
	Removed       []string         `json:"removed"`        // keys of dead entries dropped
	StaleStopped  int              `json:"stale_stopped"`  // servers stopped because their worktree is gone
	Orphans       []hygiene.Orphan `json:"orphans"`        // unregistered listeners / compose projects
	OrphansKilled int              `json:"orphans_killed"` // only with --kill-orphans
	Errors        []string         `json:"errors,omitempty"`
}

func cmdGc(env *Env, args []string) error {
	fs := newFlagSet(env, "gc")
	kill := fs.Bool("kill-orphans", false, "stop orphan listeners and compose projects")
	yes := fs.Bool("yes", false, "skip the confirmation for --kill-orphans")
	asJSON := fs.Bool("json", false, "machine-readable output")
	quiet := fs.Bool("quiet", false, "only print what was cleaned up, not orphans")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rep := GcReport{Removed: []string{}, Orphans: []hygiene.Orphan{}}

	rep.StaleStopped = env.stopStale()

	// Drop entries whose server is gone. Done under the lock so a concurrent
	// `up` that just replaced an entry is judged on its own state.
	err := env.Store.Update(func(f *registry.File) error {
		var keep []*registry.Entry
		for _, e := range f.Entries {
			if r, err := runner.Get(e.Kind); err == nil && r.Alive(*e) {
				keep = append(keep, e)
				continue
			}
			rep.Removed = append(rep.Removed, e.Key)
		}
		f.Entries = keep
		return nil
	})
	if err != nil {
		return err
	}

	// Scan listeners BEFORE reading the registry: `up` registers a server in
	// the same locked section that starts it, so a listener seen first is
	// already registered by the time we read. The other order would flag a
	// server started in between as an orphan.
	sc := hygiene.Scanner{}
	listeners, lerr := sc.Listeners()
	if lerr != nil {
		rep.Errors = append(rep.Errors, "listener scan: "+lerr.Error())
	}
	f, err := env.Store.Load()
	if err != nil {
		return err
	}
	registered, composes, groups := map[int]bool{}, map[string]bool{}, map[int]bool{}
	for _, e := range f.Entries {
		if e.PGID > 0 {
			groups[e.PGID] = true
		}
		for _, p := range e.Ports {
			registered[p] = true
		}
		if e.ComposeProject != "" {
			composes[e.ComposeProject] = true
		}
	}
	min, max := env.Alloc.Min, env.Alloc.Max
	if min == 0 {
		min, max = 41000, 41999
	}
	// A listener inside a registered server's process group is that server
	// (possibly drifted off its allocated port), not an orphan.
	var strays []hygiene.Listener
	for _, l := range listeners {
		if !groups[l.PGID] {
			strays = append(strays, l)
		}
	}
	rep.Orphans = append(rep.Orphans, hygiene.PortOrphans(strays, registered, min, max)...)
	projects, cerr := hygiene.Compose().Find(composes)
	if cerr != nil {
		rep.Errors = append(rep.Errors, "compose scan: "+cerr.Error())
	}
	for _, p := range projects {
		rep.Orphans = append(rep.Orphans, hygiene.Orphan{Kind: "compose", Project: p})
	}

	if *kill && len(rep.Orphans) > 0 {
		rep.OrphansKilled = env.killOrphans(rep.Orphans, *yes, &rep)
	}

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	for _, k := range rep.Removed {
		fmt.Fprintln(env.Stderr, "devrun: removed dead entry", k)
	}
	if !*quiet {
		for _, o := range rep.Orphans {
			switch o.Kind {
			case "port":
				fmt.Fprintf(env.Stderr, "devrun: orphan: port %d held by pid %d (not in registry)\n", o.Port, o.PID)
			case "compose":
				fmt.Fprintf(env.Stderr, "devrun: orphan: compose project %s (not in registry)\n", o.Project)
			}
		}
		if len(rep.Orphans) > 0 && !*kill {
			fmt.Fprintln(env.Stderr, "devrun: pass --kill-orphans to stop them")
		}
	}
	for _, e := range rep.Errors {
		fmt.Fprintln(env.Stderr, "devrun: warning:", e)
	}
	if len(rep.Removed) == 0 && rep.StaleStopped == 0 && len(rep.Orphans) == 0 && !*quiet {
		fmt.Fprintln(env.Stderr, "devrun: nothing to clean")
	}
	return nil
}

// killOrphans stops the given orphans and returns how many it stopped. A port
// orphan is killed only if it belongs to the current user, still has the start
// time seen at scan time, and its port is still unregistered (re-checked under
// the registry lock's snapshot right before the kill).
func (env *Env) killOrphans(orphans []hygiene.Orphan, yes bool, rep *GcReport) int {
	if !yes {
		fmt.Fprintln(env.Stderr, "About to stop orphans:")
		for _, o := range orphans {
			if o.Kind == "port" {
				fmt.Fprintf(env.Stderr, "  port %d (pid %d)\n", o.Port, o.PID)
			} else {
				fmt.Fprintf(env.Stderr, "  compose project %s\n", o.Project)
			}
		}
		if !env.confirm("Stop them?") {
			rep.Errors = append(rep.Errors, "orphans left alone (not confirmed; pass --yes)")
			return 0
		}
	}
	// Re-read the registry: anything registered since the scan is not an orphan.
	f, err := env.Store.Load()
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return 0
	}
	registered := map[int]bool{}
	for _, e := range f.Entries {
		for _, p := range e.Ports {
			registered[p] = true
		}
	}
	n := 0
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].Port < orphans[j].Port })
	for _, o := range orphans {
		switch o.Kind {
		case "port":
			if registered[o.Port] {
				continue
			}
			if !ownedByUs(o.PID) {
				rep.Errors = append(rep.Errors, fmt.Sprintf("not killing pid %d on port %d: owned by another user", o.PID, o.Port))
				continue
			}
			ok, err := hygiene.Scanner{}.Kill(o.Listen, StopGrace)
			switch {
			case err != nil:
				rep.Errors = append(rep.Errors, fmt.Sprintf("killing pid %d: %v", o.PID, err))
			case !ok:
				rep.Errors = append(rep.Errors, fmt.Sprintf("pid %d on port %d changed since the scan; left alone", o.PID, o.Port))
			default:
				n++
			}
		case "compose":
			if err := hygiene.Compose().Down(o.Project); err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("compose down %s: %v", o.Project, err))
			} else {
				n++
			}
		}
	}
	return n
}

func ownedByUs(pid int) bool {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
