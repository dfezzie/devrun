package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/exterkamp/devrun/internal/registry"
)

// confirm asks a yes/no question on stderr and reads the answer from stdin.
// No stdin or EOF means no.
func (env *Env) confirm(prompt string) bool {
	if env.Stdin == nil {
		return false
	}
	fmt.Fprintf(env.Stderr, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// stopScoped stops every server matching the scope. Each one goes through its
// runner's Stop (via stopEntry), so process and docker kinds both work, and the
// per-kind PID-reuse guards apply.
func (env *Env) stopScoped(match func(*registry.Entry) bool, what string, yes bool) error {
	f, err := env.Store.Load()
	if err != nil {
		return err
	}
	var targets []registry.Entry
	for _, e := range f.Entries {
		if match(e) {
			targets = append(targets, *e)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintf(env.Stderr, "devrun: no servers for %s\n", what)
		return nil
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Key < targets[j].Key })
	if !yes {
		fmt.Fprintf(env.Stderr, "About to stop %d server(s) for %s:\n", len(targets), what)
		for _, e := range targets {
			fmt.Fprintf(env.Stderr, "  %s/%s %s (port %d, %s)\n", e.Project, filepath.Base(e.Worktree), e.Name, e.Port(), e.Kind)
		}
		if !env.confirm("Stop them?") {
			return exitError{code: 1, msg: "aborted (pass --yes to skip confirmation)"}
		}
	}
	var errs []string
	for _, e := range targets {
		if err := env.stopEntry(e); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// cleanPath normalises a directory for comparison with Entry.Worktree.
func cleanPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// stopWide handles the scoped forms of `stop`.
func (env *Env) stopWide(project, worktree string, all, yes bool, pos []string) error {
	n := 0
	for _, set := range []bool{project != "", worktree != "", all} {
		if set {
			n++
		}
	}
	if n > 1 || len(pos) > 0 {
		return errors.New("use exactly one of: stop [name] | --project P | --worktree DIR | --all")
	}
	switch {
	case all:
		return env.stopScoped(func(*registry.Entry) bool { return true }, "all projects", yes)
	case project != "":
		return env.stopScoped(func(e *registry.Entry) bool { return e.Project == project }, "project "+project, yes)
	}
	wt := cleanPath(worktree)
	return env.stopScoped(func(e *registry.Entry) bool { return cleanPath(e.Worktree) == wt }, "worktree "+wt, yes)
}
