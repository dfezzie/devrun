package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/exterkamp/devrun/internal/docker"
	"github.com/exterkamp/devrun/internal/hygiene"
	"github.com/exterkamp/devrun/internal/proc"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// recRunner is a kind that records Stop calls.
type recRunner struct {
	mu      sync.Mutex
	alive   bool
	stopped []string
}

func (*recRunner) Kind() string                             { return "rec" }
func (*recRunner) Start(runner.Spec) (runner.Handle, error) { return runner.Handle{}, nil }
func (r *recRunner) Alive(registry.Entry) bool              { return r.alive }
func (r *recRunner) Stop(e registry.Entry, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, e.Name)
	return nil
}
func (*recRunner) Logs(context.Context, registry.Entry, runner.LogOpts, io.Writer) error { return nil }

func useRec(t *testing.T, alive bool) *recRunner {
	r := &recRunner{alive: alive}
	runner.Register(r)
	return r
}

func put(t *testing.T, env *Env, es ...registry.Entry) {
	t.Helper()
	env.Store.Update(func(f *registry.File) error {
		for i := range es {
			e := es[i]
			if e.Key == "" {
				e.Key = registry.Key(e.Project, e.Worktree, e.Name)
			}
			if e.Kind == "" {
				e.Kind = "rec"
			}
			e.StartedAt = time.Now()
			f.Upsert(&e)
		}
		return nil
	})
}

func keys(t *testing.T, env *Env) []string {
	f, err := env.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var ks []string
	for _, e := range f.Entries {
		ks = append(ks, e.Name)
	}
	return ks
}

func TestStaleWorktreeStoppedOnLsAndGc(t *testing.T) {
	for _, cmd := range []string{"ls", "gc"} {
		rec := useRec(t, true)
		env, _ := testEnv(t, nil)
		here := t.TempDir()
		gone := filepath.Join(t.TempDir(), "removed-worktree")
		put(t, env,
			registry.Entry{Project: "p", Worktree: here, Name: "keep", Ports: map[string]int{"PORT": 41001}},
			registry.Entry{Project: "p", Worktree: gone, Name: "stale", Ports: map[string]int{"PORT": 41002}})
		if code := Run(env, []string{cmd}); code != 0 {
			t.Fatalf("%s: exit %d", cmd, code)
		}
		if len(rec.stopped) != 1 || rec.stopped[0] != "stale" {
			t.Fatalf("%s: stopped %v", cmd, rec.stopped)
		}
		if ks := keys(t, env); len(ks) != 1 || ks[0] != "keep" {
			t.Fatalf("%s: registry %v", cmd, ks)
		}
	}
}

func TestGcDropsDeadKeepsLive(t *testing.T) {
	env, _ := testEnv(t, nil)
	rec := useRec(t, false)
	wt := t.TempDir()
	put(t, env, registry.Entry{Project: "p", Worktree: wt, Name: "dead", Ports: map[string]int{"PORT": 41001}})
	rec.alive = true
	put(t, env, registry.Entry{Project: "p", Worktree: wt, Name: "live", Kind: "rec", Ports: map[string]int{"PORT": 41002}})
	rec.alive = false
	// "rec" reports one liveness for all; use a second kind for the live one.
	runner.Register(fakeRunner{alive: true})
	put(t, env, registry.Entry{Project: "p", Worktree: wt, Name: "live2", Kind: "fake", Ports: map[string]int{"PORT": 41003}})
	if Run(env, []string{"gc"}) != 0 {
		t.Fatal("gc failed")
	}
	if ks := keys(t, env); len(ks) != 1 || ks[0] != "live2" {
		t.Fatalf("registry %v", ks)
	}
}

func TestGcReportsOrphansAndKillsOnlyWhenAsked(t *testing.T) {
	useRec(t, true)
	env, out := testEnv(t, nil)
	l, err := net.Listen("tcp", "127.0.0.1:0") // owned by this test process
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	env.Alloc.Min, env.Alloc.Max = port, port

	if Run(env, []string{"gc", "--json"}) != 0 {
		t.Fatal("gc failed")
	}
	var rep GcReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Orphans) != 1 || rep.Orphans[0].Port != port || rep.Orphans[0].PID != os.Getpid() {
		t.Fatalf("orphans %+v", rep.Orphans)
	}

	// Registering the port makes it not an orphan.
	put(t, env, registry.Entry{Project: "p", Worktree: t.TempDir(), Name: "web", Ports: map[string]int{"PORT": port}})
	out.Reset()
	Run(env, []string{"gc", "--json"})
	rep = GcReport{}
	json.Unmarshal(out.Bytes(), &rep)
	if len(rep.Orphans) != 0 {
		t.Fatalf("registered port reported as orphan: %+v", rep.Orphans)
	}
}

func TestKillOrphansNeverKillsItselfAndNeedsConfirmation(t *testing.T) {
	useRec(t, true)
	env, out := testEnv(t, nil)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	env.Alloc.Min, env.Alloc.Max = port, port

	env.Stdin = strings.NewReader("n\n")
	Run(env, []string{"gc", "--kill-orphans", "--json"})
	var rep GcReport
	json.Unmarshal(out.Bytes(), &rep)
	if rep.OrphansKilled != 0 {
		t.Fatal("killed without confirmation")
	}
	// With --yes the orphan is this very process: Kill must refuse (we are
	// still here to assert it).
	out.Reset()
	Run(env, []string{"gc", "--kill-orphans", "--yes", "--json"})
	rep = GcReport{}
	json.Unmarshal(out.Bytes(), &rep)
	if rep.OrphansKilled != 0 {
		t.Fatalf("report %+v", rep)
	}
}

type fakeCompose struct{ downed []string }

func (f *fakeCompose) Find(known map[string]bool) ([]string, error) {
	var o []string
	for _, p := range []string{"devrun-known", "devrun-stray"} {
		if !known[p] {
			o = append(o, p)
		}
	}
	return o, nil
}
func (f *fakeCompose) Down(p string) error { f.downed = append(f.downed, p); return nil }

func TestComposeOrphansThroughInterface(t *testing.T) {
	useRec(t, true)
	fc := &fakeCompose{}
	hygiene.SetCompose(fc)
	defer hygiene.SetCompose(nil)
	env, out := testEnv(t, nil)
	env.Alloc.Min, env.Alloc.Max = 1, 1
	put(t, env, registry.Entry{Project: "p", Worktree: t.TempDir(), Name: "d", ComposeProject: "devrun-known", Ports: map[string]int{"PORT": 41001}})
	Run(env, []string{"gc", "--json"})
	var rep GcReport
	json.Unmarshal(out.Bytes(), &rep)
	if len(rep.Orphans) != 1 || rep.Orphans[0].Project != "devrun-stray" || len(fc.downed) != 0 {
		t.Fatalf("%+v %v", rep, fc.downed)
	}
	out.Reset()
	Run(env, []string{"gc", "--kill-orphans", "--yes", "--json"})
	if len(fc.downed) != 1 || fc.downed[0] != "devrun-stray" {
		t.Fatalf("downed %v", fc.downed)
	}
}

func TestDriftWarningInLs(t *testing.T) {
	useRec(t, true)
	old := groupPorts
	defer func() { groupPorts = old }()
	groupPorts = func(pgid int) ([]int, error) {
		if pgid != 4242 {
			t.Fatalf("scanned group %d", pgid)
		}
		return []int{41555}, nil
	}
	env, out := testEnv(t, map[int]bool{41555: true})
	wt := t.TempDir()
	put(t, env,
		registry.Entry{Project: "p", Worktree: wt, Name: "drifted", PID: 4242, PGID: 4242, Ports: map[string]int{"PORT": 41001}},
		registry.Entry{Project: "p", Worktree: wt, Name: "fine", PID: 4243, PGID: 4243, Ports: map[string]int{"PORT": 41555}})
	Run(env, []string{"ls", "--json"})
	var vs []View
	if err := json.Unmarshal(out.Bytes(), &vs); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		switch v.Name {
		case "drifted":
			if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "41555") {
				t.Fatalf("warnings %v", v.Warnings)
			}
		case "fine":
			if len(v.Warnings) != 0 {
				t.Fatalf("false drift: %v", v.Warnings)
			}
		}
	}
}

func TestDriftNotCheckedForDeadServer(t *testing.T) {
	useRec(t, false)
	old := groupPorts
	defer func() { groupPorts = old }()
	groupPorts = func(int) ([]int, error) { t.Fatal("scanned the group of a dead server"); return nil, nil }
	e := &registry.Entry{Kind: "rec", PID: 1, PGID: 1, Ports: map[string]int{"PORT": 41001}}
	if got := driftPorts(e, false); got != nil {
		t.Fatal(got)
	}
}

func TestStopScopedConfirmation(t *testing.T) {
	rec := useRec(t, true)
	env, _ := testEnv(t, nil)
	wt := t.TempDir()
	put(t, env,
		registry.Entry{Project: "a", Worktree: wt, Name: "a1", Ports: map[string]int{"PORT": 41001}},
		registry.Entry{Project: "a", Worktree: wt, Name: "a2", Ports: map[string]int{"PORT": 41002}},
		registry.Entry{Project: "b", Worktree: wt, Name: "b1", Ports: map[string]int{"PORT": 41003}})

	env.Stdin = strings.NewReader("n\n")
	if Run(env, []string{"stop", "--project", "a"}) == 0 || len(rec.stopped) != 0 {
		t.Fatal("stopped without confirmation")
	}
	env.Stdin = nil
	if Run(env, []string{"stop", "--all"}) == 0 || len(rec.stopped) != 0 {
		t.Fatal("stopped with no stdin")
	}
	env.Stdin = strings.NewReader("y\n")
	if Run(env, []string{"stop", "--project", "a"}) != 0 || len(rec.stopped) != 2 {
		t.Fatalf("stopped %v", rec.stopped)
	}
	if ks := keys(t, env); len(ks) != 1 || ks[0] != "b1" {
		t.Fatalf("registry %v", ks)
	}
	if Run(env, []string{"stop", "--all", "--yes"}) != 0 || len(rec.stopped) != 3 || len(keys(t, env)) != 0 {
		t.Fatalf("stopped %v", rec.stopped)
	}
	if Run(env, []string{"stop", "--all", "--project", "x", "--yes"}) == 0 {
		t.Fatal("conflicting scopes accepted")
	}
}

func TestStopWorktreeScope(t *testing.T) {
	rec := useRec(t, true)
	env, _ := testEnv(t, nil)
	w1, w2 := t.TempDir(), t.TempDir()
	put(t, env,
		registry.Entry{Project: "a", Worktree: w1, Name: "one", Ports: map[string]int{"PORT": 41001}},
		registry.Entry{Project: "a", Worktree: w2, Name: "two", Ports: map[string]int{"PORT": 41002}})
	if Run(env, []string{"stop", "--worktree", w1, "--yes"}) != 0 || len(rec.stopped) != 1 || rec.stopped[0] != "one" {
		t.Fatalf("stopped %v", rec.stopped)
	}
}

// The proc kind must never signal a process that merely reuses a recorded PID.
func TestPIDReuseGuardThroughStopAndGc(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // its own group, like a devrun server
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	start, ok := proc.StartTime(cmd.Process.Pid)
	if !ok {
		t.Fatal("no start time")
	}
	env, _ := testEnv(t, nil)
	wt := t.TempDir()
	// The registry remembers a different (long dead) process under this PID.
	stale := registry.Entry{Project: "p", Worktree: wt, Name: "old", Kind: proc.Kind,
		PID: cmd.Process.Pid, PGID: cmd.Process.Pid, PIDStart: start + 12345,
		Ports: map[string]int{"PORT": 41001}}
	put(t, env, stale)

	if Run(env, []string{"gc"}) != 0 {
		t.Fatal("gc failed")
	}
	if len(keys(t, env)) != 0 {
		t.Fatal("entry with mismatched start time should be dropped as dead")
	}
	put(t, env, stale)
	if Run(env, []string{"stop", "--all", "--yes"}) != 0 {
		t.Fatal("stop failed")
	}
	if _, ok := proc.StartTime(cmd.Process.Pid); !ok {
		t.Fatal("unrelated process was killed through a reused PID")
	}
	// ...and the genuine entry (matching start time) does get stopped.
	live := stale
	live.PIDStart = start
	put(t, env, live)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	if Run(env, []string{"stop", "--all", "--yes"}) != 0 {
		t.Fatal("stop failed")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("matching process not stopped")
	}
}

func TestHookInstall(t *testing.T) {
	env, out := testEnv(t, nil)
	if err := exec.Command("git", "init", "-q", env.Cwd).Run(); err != nil {
		t.Skip("git unavailable")
	}
	if Run(env, []string{"hook", "install"}) != 0 {
		t.Fatal("install failed")
	}
	hook := filepath.Join(env.Cwd, ".git", "hooks", "post-checkout")
	fi, err := os.Stat(hook)
	if err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("hook missing or not executable: %v", err)
	}
	if !strings.Contains(out.String(), "gwt-rm()") || !strings.Contains(out.String(), "devrun stop --worktree") {
		t.Fatalf("wrapper not printed: %q", out.String())
	}
	// Idempotent over our own hook; refuses to clobber a foreign one.
	if Run(env, []string{"hook", "install"}) != 0 {
		t.Fatal("reinstall failed")
	}
	os.WriteFile(hook, []byte("#!/bin/sh\necho mine\n"), 0o755)
	if Run(env, []string{"hook", "install"}) == 0 {
		t.Fatal("overwrote a foreign hook")
	}
	if Run(env, []string{"hook", "install", "--force"}) != 0 {
		t.Fatal("--force failed")
	}
}

func TestDriftedPortIsNotAnOrphan(t *testing.T) {
	useRec(t, true)
	env, out := testEnv(t, nil)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	env.Alloc.Min, env.Alloc.Max = port, port
	// A registered server whose group is ours (this test process) listens on a
	// port that is not the one allocated to it.
	put(t, env, registry.Entry{Project: "p", Worktree: t.TempDir(), Name: "web",
		PID: os.Getpid(), PGID: syscall.Getpgrp(), Ports: map[string]int{"PORT": 41001}})
	Run(env, []string{"gc", "--json"})
	var rep GcReport
	json.Unmarshal(out.Bytes(), &rep)
	if len(rep.Orphans) != 0 {
		t.Fatalf("%+v", rep.Orphans)
	}
}

// fakeDockerCLI puts a `docker` on PATH that lists the given compose projects
// and logs every invocation's argv.
func fakeDockerCLI(t *testing.T, projects ...string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	var js []string
	for _, p := range projects {
		js = append(js, `{"Name":"`+p+`","Status":"running(1)"}`)
	}
	script := "#!/bin/sh\necho \"$*\" >> \"" + dir + "/calls.log\"\n" +
		"case \"$*\" in\n  \"compose ls\"*) echo '[" + strings.Join(js, ",") + "]' ;;\nesac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "\x00"))
	}
}

func TestGcComposeOrphansWithRealDetector(t *testing.T) {
	calls := fakeDockerCLI(t, "devrun-known-aaaaaa", "devrun-stray-bbbbbb", "paperless", "nginx_proxy_manager")
	hygiene.SetCompose(docker.Detector{})
	defer hygiene.SetCompose(nil)
	env, out := testEnv(t, nil)
	env.Alloc.Min, env.Alloc.Max = 1, 1
	useRec(t, true)
	put(t, env, registry.Entry{Project: "p", Worktree: t.TempDir(), Name: "d", ComposeProject: "devrun-known-aaaaaa", Ports: map[string]int{"PORT": 41001}})

	Run(env, []string{"gc", "--json"})
	var rep GcReport
	json.Unmarshal(out.Bytes(), &rep)
	if len(rep.Orphans) != 1 || rep.Orphans[0].Kind != "compose" || rep.Orphans[0].Project != "devrun-stray-bbbbbb" {
		t.Fatalf("%+v", rep)
	}
	for _, c := range calls() {
		if c == "down" {
			t.Fatal("down without --kill-orphans")
		}
	}

	out.Reset()
	Run(env, []string{"gc", "--kill-orphans", "--yes", "--json"})
	log := strings.Join(calls(), " ")
	if !strings.Contains(log, "-p devrun-stray-bbbbbb down") {
		t.Fatalf("no down for orphan: %s", log)
	}
	if strings.Contains(log, "paperless") || strings.Contains(log, "nginx_proxy_manager") || strings.Contains(log, "devrun-known") && strings.Contains(log, "-p devrun-known") || strings.Contains(log, "-v") || strings.Contains(log, "volumes") {
		t.Fatalf("touched something it shouldn't: %s", log)
	}
}

func TestStopScopeDispatchesDockerKindThroughRunner(t *testing.T) {
	calls := fakeDockerCLI(t)
	env, _ := testEnv(t, nil)
	wt := t.TempDir()
	put(t, env,
		registry.Entry{Project: "a", Worktree: wt, Name: "web", Kind: docker.Kind, ComposeProject: "devrun-a-cccccc",
			Cmd: []string{"docker", "compose", "up", "-d"}, Ports: map[string]int{"WEB_PORT": 41001}},
		registry.Entry{Project: "b", Worktree: wt, Name: "web", Kind: docker.Kind, ComposeProject: "devrun-b-dddddd",
			Cmd: []string{"docker", "compose", "up", "-d"}, Ports: map[string]int{"WEB_PORT": 41002}})
	if code := Run(env, []string{"stop", "--project", "a", "--yes"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	log := strings.Join(calls(), " ")
	if !strings.Contains(log, "devrun-a-cccccc") || !strings.Contains(log, "down") || strings.Contains(log, "devrun-b-dddddd") {
		t.Fatalf("calls: %s", log)
	}
	if ks := keys(t, env); len(ks) != 1 {
		t.Fatalf("registry %v", ks)
	}
	if code := Run(env, []string{"stop", "--all", "--yes"}); code != 0 || !strings.Contains(strings.Join(calls(), " "), "devrun-b-dddddd") || len(keys(t, env)) != 0 {
		t.Fatalf("stop --all: %d %v", code, calls())
	}
}

// The docker package installs a real compose detector at init. Tests must
// never scan or `down` real compose projects, so default to the no-op; tests
// that want a detector install their own (with a fake docker on PATH).
func TestMain(m *testing.M) {
	hygiene.SetCompose(nil)
	os.Exit(m.Run())
}
