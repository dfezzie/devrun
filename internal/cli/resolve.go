package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/exterkamp/devrun/internal/gitctx"
	"github.com/exterkamp/devrun/internal/registry"
)

// resolveEntry finds the server a command refers to. verb is the command name,
// used to print a copy-pasteable disambiguation. With a target argument it is
// resolved in this order:
//
//  1. a server with that name in the current worktree (only inside a git worktree)
//  2. an exact port number
//  3. project/name
//  4. a name match across all entries
//
// Matches in steps 3 and 4 that hit more than one entry are refused, never
// guessed. With no argument, inside a worktree the only server there is used;
// outside git the only server overall is used.
func (env *Env) resolveEntry(f *registry.File, verb, arg string) (*registry.Entry, error) {
	gc, err := gitctx.Resolve(env.Cwd)
	if err != nil {
		return nil, err
	}
	if arg == "" {
		return env.resolveNoArg(f, verb, gc)
	}
	if gc.InGit {
		if e := f.Find(registry.Key(gc.Project, gc.Worktree, arg)); e != nil {
			return e, nil
		}
	}
	if n, err := strconv.Atoi(arg); err == nil {
		var hits []*registry.Entry
		for _, e := range f.Entries {
			for _, p := range e.Ports {
				if p == n {
					hits = append(hits, e)
					break
				}
			}
		}
		if len(hits) > 0 {
			return env.pickOne(hits, verb, arg)
		}
	}
	if proj, name, ok := strings.Cut(arg, "/"); ok {
		var hits []*registry.Entry
		for _, e := range f.Entries {
			if e.Project == proj && e.Name == name {
				hits = append(hits, e)
			}
		}
		if len(hits) > 0 {
			return env.pickOne(hits, verb, arg)
		}
	}
	var hits []*registry.Entry
	for _, e := range f.Entries {
		if e.Name == arg {
			hits = append(hits, e)
		}
	}
	if len(hits) > 0 {
		return env.pickOne(hits, verb, arg)
	}
	return nil, notFound(f, arg, gc)
}

func (env *Env) resolveNoArg(f *registry.File, verb string, gc gitctx.Context) (*registry.Entry, error) {
	if !gc.InGit {
		switch len(f.Entries) {
		case 0:
			return nil, fmt.Errorf("no servers registered")
		case 1:
			return f.Entries[0], nil
		}
		return nil, ambiguous(f.Entries, verb, "not in a git worktree and several servers exist; pick one")
	}
	var here []*registry.Entry
	for _, e := range f.Entries {
		if e.Worktree == gc.Worktree && e.Project == gc.Project {
			here = append(here, e)
		}
	}
	switch len(here) {
	case 0:
		return nil, fmt.Errorf("no server registered for %s (%s)", gc.Project, gc.Worktree)
	case 1:
		return here[0], nil
	}
	names := make([]string, len(here))
	for i, e := range here {
		names[i] = e.Name
	}
	sort.Strings(names)
	return nil, fmt.Errorf("several servers here (%s); pass a name", strings.Join(names, ", "))
}

func (env *Env) pickOne(hits []*registry.Entry, verb, arg string) (*registry.Entry, error) {
	if len(hits) == 1 {
		return hits[0], nil
	}
	return nil, ambiguous(hits, verb, fmt.Sprintf("%q matches %d servers; nothing done", arg, len(hits)))
}

func sortedEntries(es []*registry.Entry) []*registry.Entry {
	out := append([]*registry.Entry(nil), es...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Worktree != b.Worktree {
			return a.Worktree < b.Worktree
		}
		return a.Name < b.Name
	})
	return out
}

// ambiguous refuses to act: it lists the candidates with the exact command
// (by port, which is unique) to pick each one.
func ambiguous(es []*registry.Entry, verb, why string) error {
	var b strings.Builder
	b.WriteString(why + ":\n")
	for _, e := range sortedEntries(es) {
		fmt.Fprintf(&b, "  %s  worktree %s  name %s  port %d  ->  devrun %s %d\n",
			e.Project, e.Worktree, e.Name, e.Port(), verb, e.Port())
	}
	return exitError{code: 1, msg: strings.TrimRight(b.String(), "\n")}
}

func notFound(f *registry.File, arg string, gc gitctx.Context) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no server matching %q (searched: ", arg)
	if gc.InGit {
		fmt.Fprintf(&b, "name in worktree %s, ", gc.Worktree)
	}
	b.WriteString("port number, project/name, name in all projects)")
	if len(f.Entries) == 0 {
		b.WriteString("\nno servers are registered")
	} else {
		b.WriteString("\ncurrent servers:")
		for _, e := range sortedEntries(f.Entries) {
			fmt.Fprintf(&b, "\n  %s/%s  port %d  (%s)", e.Project, e.Name, e.Port(), filepath.Base(e.Worktree))
		}
	}
	return exitError{code: 1, msg: b.String()}
}
