package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/exterkamp/devrun/internal/hygiene"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// driftGrace is how long a stray listener must persist, with the allocated
// port still silent, before `up` calls it drift (an inspector or helper port
// can open before the real server binds).
var driftGrace = 3 * time.Second

// groupPorts lists the ports a process group listens on (a var so tests can
// substitute it).
var groupPorts = func(pgid int) ([]int, error) { return hygiene.Scanner{}.GroupPorts(pgid) }

// driftPorts detects a framework that ignored its port: the server is alive,
// its allocated primary port is not listening, yet its process group listens
// on other ports. It returns those other ports.
//
// Only a live server (PID and start time match, so the group really is ours)
// is inspected, which also keeps this from ever reading a recycled group.
func driftPorts(e *registry.Entry, primaryListening bool) []int {
	if primaryListening || e.PGID <= 0 || e.Port() == 0 {
		return nil
	}
	if r, err := runner.Get(e.Kind); err != nil || !r.Alive(*e) {
		return nil
	}
	ports, err := groupPorts(e.PGID)
	if err != nil {
		return nil
	}
	own := map[int]bool{}
	for _, p := range e.Ports {
		own[p] = true
	}
	var extra []int
	for _, p := range ports {
		if !own[p] {
			extra = append(extra, p)
		}
	}
	sort.Ints(extra)
	return extra
}

func driftMessage(e *registry.Entry, extra []int) string {
	return fmt.Sprintf("%s ignored its port %d and is listening on %v instead (pass the port with --port-env or {port})",
		e.Name, e.Port(), extra)
}
