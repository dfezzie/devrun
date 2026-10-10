package cli

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/exterkamp/devrun/internal/gitctx"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

func init() {
	Register(Command{Name: "up", Summary: "start a server (idempotent): up [--name web] [--port-env PORT] [--wait 60s] -- <cmd...> | up --compose [--ports web,api] [--port-var web=VAR] [-f file] [--build]", Run: cmdUp})
}

// UpPlan describes what a start mode wants to run.
type UpPlan struct {
	Kind      string   // runner kind
	Cmd       []string // recorded in the registry; meaning is up to the runner
	PortNames []string // one port is allocated per name
	Primary   string   // which port name the URL points at
}

// UpMode is a way of starting servers (plain process, docker, ...). Bind
// registers the mode's flags on fs and returns a selector that, after parsing,
// reports whether this mode was requested. The first selecting mode wins; the
// proc mode is the fallback. Modes register from init in their own file.
type UpMode struct {
	Name string
	Bind func(fs *flag.FlagSet) (selected func(rest []string) (*UpPlan, error))
}

var upModes []UpMode

// RegisterUpMode adds a start mode. Call from init.
func RegisterUpMode(m UpMode) { upModes = append(upModes, m) }

func cmdUp(env *Env, args []string) error {
	fs := newFlagSet(env, "up")
	name := fs.String("name", DefaultName, "server name within this worktree")
	wait := fs.Duration("wait", 60*time.Second, "how long to wait for the port to accept connections")
	// Registration order follows file names, so pin the proc fallback last.
	modes := append([]UpMode(nil), upModes...)
	sort.SliceStable(modes, func(i, j int) bool { return modes[j].Name == "proc" && modes[i].Name != "proc" })
	selectors := make([]func([]string) (*UpPlan, error), len(modes))
	for i, m := range modes {
		selectors[i] = m.Bind(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	var plan *UpPlan
	for _, sel := range selectors {
		p, err := sel(fs.Args())
		if err != nil {
			return err
		}
		if p != nil {
			plan = p
			break
		}
	}
	if plan == nil {
		return errors.New("no start mode selected")
	}

	gc, err := gitctx.Resolve(env.Cwd)
	if err != nil {
		return err
	}
	key := registry.Key(gc.Project, gc.Worktree, *name)
	rn, err := runner.Get(plan.Kind)
	if err != nil {
		return err
	}

	owner := detectOwner() // before the lock: it may call herdr

	// Under the lock: reuse a live server, or allocate a port and start one.
	var entry registry.Entry
	var started bool
	err = env.Store.Update(func(f *registry.File) error {
		if old := f.Find(key); old != nil {
			if r, err := runner.Get(old.Kind); err == nil && r.Alive(*old) {
				entry = *old
				return nil
			}
		}
		ports, err := env.Alloc.Allocate(f, key, plan.PortNames)
		if err != nil {
			return err
		}
		e := registry.Entry{
			Key: key, Project: gc.Project, Worktree: gc.Worktree, Branch: gc.Branch,
			Name: *name, Kind: rn.Kind(), Cmd: plan.Cmd, Ports: ports, PrimaryPort: plan.Primary,
			StartedAt: env.Now().UTC(), Owner: owner, Log: env.logPath(gc.Project, gc.Worktree, *name),
		}
		h, err := rn.Start(runner.Spec{Entry: e, Dir: gc.Worktree, Primary: plan.Primary})
		if err != nil {
			return err
		}
		e.PID, e.PGID, e.PIDStart, e.ComposeProject = h.PID, h.PGID, h.PIDStart, h.ComposeProject
		f.Upsert(&e)
		entry, started = e, true
		return nil
	})
	if err != nil {
		return err
	}

	// Outside the lock: wait for the server to come up.
	if err := waitListening(env, rn, &entry, *wait); err != nil {
		if started {
			// Don't leave a half-started server behind a failing `up`.
			rn.Stop(entry, 5*time.Second)
			env.Store.Update(func(f *registry.File) error {
				if cur := f.Find(key); cur != nil && cur.PID == entry.PID {
					f.Remove(key)
				}
				return nil
			})
		}
		return err
	}
	if started {
		fmt.Fprintln(env.Stderr, "devrun: started", *name, "pid", entry.PID, "log", entry.Log)
	} else {
		fmt.Fprintln(env.Stderr, "devrun: already running", *name, "pid", entry.PID)
	}
	fmt.Fprintln(env.Stdout, entry.URL())
	return nil
}

func waitListening(env *Env, rn runner.Runner, e *registry.Entry, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(e.Port()))
	var lastDrift, driftSince time.Time
	for {
		if !rn.Alive(*e) {
			return fmt.Errorf("%s exited before listening on port %d; last log lines:\n%s",
				e.Name, e.Port(), lastLogLines(e.Log))
		}
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Since(lastDrift) > 500*time.Millisecond {
			lastDrift = time.Now()
			if extra := driftPorts(e, false); len(extra) == 0 {
				driftSince = time.Time{}
			} else if driftSince.IsZero() {
				driftSince = time.Now()
			} else if time.Since(driftSince) >= driftGrace {
				// The server is up, just not where we put it. Warn, and report the
				// port it actually bound (the registry keeps the allocated one).
				fmt.Fprintln(env.Stderr, "devrun: warning:", driftMessage(e, extra))
				ports := make(map[string]int, len(e.Ports))
				for n, p := range e.Ports {
					if p == e.Port() {
						p = extra[0]
					}
					ports[n] = p
				}
				e.Ports = ports
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not listen on port %d within %s (it may ignore $PORT; use --port-env or {port})", e.Name, e.Port(), wait)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func lastLogLines(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	const max = 2000
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return string(data)
}
