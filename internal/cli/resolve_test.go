package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exterkamp/devrun/internal/registry"
)

func gitRepo(t *testing.T, name string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(root, name)
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return dir
}

func entry(project, wt, name string, port int) *registry.Entry {
	return &registry.Entry{Key: registry.Key(project, wt, name), Project: project, Worktree: wt, Name: name,
		Kind: "fake", Ports: map[string]int{"PORT": port}}
}

func TestResolveEntryTable(t *testing.T) {
	repo := gitRepo(t, "app")
	nonRepo, _ := filepath.EvalSymlinks(t.TempDir())
	f := &registry.File{}
	for _, e := range []*registry.Entry{
		entry("app", repo, "web", 41001),
		entry("app", "/elsewhere/app-wt", "web", 41002),
		entry("app", repo, "api", 41003),
		entry("other", "/o", "frontend", 41004),
		entry("other", "/o", "db", 41005),
		entry("third", "/t1", "db", 41006),
		entry("third", "/t2", "db", 41007),
	} {
		f.Upsert(e)
	}
	env, _ := testEnv(t, nil)

	cases := []struct {
		name    string
		cwd     string
		arg     string
		want    int    // expected port, 0 if error expected
		errHas  string // substring of error
		errCode int
	}{
		{"1 name in current worktree", repo, "web", 41001, "", 0},
		{"1 miss falls through to ambiguous global", repo, "db", 0, "matches 3", 1},
		{"1 worktree name wins over port-like", repo, "api", 41003, "", 0},
		{"2 exact port from inside repo", repo, "41004", 41004, "", 0},
		{"2 exact port from outside", nonRepo, "41002", 41002, "", 0},
		{"3 project/name", nonRepo, "other/db", 41005, "", 0},
		{"3 project/name ambiguous across worktrees", nonRepo, "third/db", 0, "matches 2", 1},
		{"3 project/name ambiguous across worktrees (app)", nonRepo, "app/web", 0, "matches 2", 1},
		{"4 global unique name from outside", nonRepo, "frontend", 41004, "", 0},
		{"4 global unique name from other repo", repo, "frontend", 41004, "", 0},
		{"4 bare name ambiguous from outside", nonRepo, "web", 0, "devrun stop 41001", 1},
		{"4 bare name ambiguous lists ports", nonRepo, "db", 0, "devrun stop 41007", 1},
		{"not found outside lists servers", nonRepo, "nope", 0, "other/frontend  port 41004", 1},
		{"not found says what was searched", nonRepo, "nope", 0, "searched: port number, project/name, name in all projects", 1},
		{"not found in repo mentions worktree", repo, "nope", 0, "name in worktree " + repo, 1},
		{"unknown port", nonRepo, "41999", 0, "no server matching", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env.Cwd = c.cwd
			e, err := env.resolveEntry(f, "stop", c.arg)
			if c.errHas == "" {
				if err != nil || e.Port() != c.want {
					t.Fatalf("got %v, %v; want port %d", e, err, c.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Fatalf("err = %v; want containing %q", err, c.errHas)
			}
			if ee, ok := err.(exitError); !ok || ee.code != c.errCode {
				t.Fatalf("err %T %v: want exit code %d", err, err, c.errCode)
			}
			if strings.Contains(err.Error(), "project "+filepath.Base(c.cwd)) {
				t.Fatalf("non-repo dir called a project: %v", err)
			}
		})
	}
}

func TestResolveEntryNoArg(t *testing.T) {
	repo := gitRepo(t, "app")
	nonRepo, _ := filepath.EvalSymlinks(t.TempDir())
	env, _ := testEnv(t, nil)

	one := &registry.File{}
	one.Upsert(entry("x", "/x", "web", 41010))
	many := &registry.File{}
	many.Upsert(entry("x", "/x", "web", 41010))
	many.Upsert(entry("y", "/y", "web", 41011))
	empty := &registry.File{}
	here := &registry.File{}
	here.Upsert(entry("app", repo, "web", 41012))

	env.Cwd = nonRepo
	if e, err := env.resolveEntry(one, "stop", ""); err != nil || e.Port() != 41010 {
		t.Fatalf("single server outside git: %v %v", e, err)
	}
	if _, err := env.resolveEntry(many, "stop", ""); err == nil || !strings.Contains(err.Error(), "devrun stop 41011") {
		t.Fatalf("several outside git must list: %v", err)
	}
	if _, err := env.resolveEntry(empty, "stop", ""); err == nil {
		t.Fatal("no servers must error")
	}
	env.Cwd = repo
	if e, err := env.resolveEntry(here, "stop", ""); err != nil || e.Port() != 41012 {
		t.Fatalf("worktree server: %v %v", e, err)
	}
	if _, err := env.resolveEntry(one, "stop", ""); err == nil {
		t.Fatal("no server here must error, not fall back to another project's")
	}
}

func TestStopRefusesAmbiguousAndActsOnPort(t *testing.T) {
	nonRepo, _ := filepath.EvalSymlinks(t.TempDir())
	env, _ := testEnv(t, nil)
	env.Cwd = nonRepo
	addServerAt(t, env, "a", "web", 41021)
	addServerAt(t, env, "b", "web", 41022)

	if Run(env, []string{"stop", "web"}) == 0 {
		t.Fatal("ambiguous stop must fail")
	}
	f, _ := env.Store.Load()
	if len(f.Entries) != 2 {
		t.Fatal("ambiguous stop must not act")
	}
	if Run(env, []string{"stop", "41022"}) != 0 {
		t.Fatal(env.Stderr)
	}
	f, _ = env.Store.Load()
	if len(f.Entries) != 1 || f.Entries[0].Project != "a" {
		t.Fatalf("wrong entry removed: %+v", f.Entries)
	}
}

func addServerAt(t *testing.T, env *Env, project, name string, port int) {
	t.Helper()
	if err := env.Store.Update(func(f *registry.File) error {
		f.Upsert(entry(project, "/w/"+project, name, port))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
