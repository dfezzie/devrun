package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEndTargetResolutionAcrossWorktrees(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	main := initRepo(t, root, "proj")
	wt2 := filepath.Join(root, "proj-wt2")
	for _, args := range [][]string{
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "-b", "wt2", wt2},
	} {
		if out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	outside := root // a directory that is not a git repo
	t.Cleanup(func() { runIn(t, bin, state, main, "stop"); runIn(t, bin, state, wt2, "stop") })

	srv := []string{"up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1"}
	u1, u2 := runIn(t, bin, state, main, srv...), runIn(t, bin, state, wt2, srv...)
	if u1.code != 0 || u2.code != 0 || u1.out == u2.out {
		t.Fatalf("u1=%+v u2=%+v", u1, u2)
	}
	p1, p2 := portOf(t, u1.out), portOf(t, u2.out)

	// from outside any repo a bare name is ambiguous: refuse, list, touch nothing
	r := runIn(t, bin, state, outside, "stop", "web")
	if r.code == 0 || !strings.Contains(r.err, "matches 2") || !strings.Contains(r.err, "devrun stop ") {
		t.Fatalf("outside stop web: %+v", r)
	}
	if !listening(p1) || !listening(p2) {
		t.Fatal("ambiguous stop killed something")
	}
	// no argument outside a repo with several servers: refuse too
	if r := runIn(t, bin, state, outside, "stop"); r.code == 0 || !listening(p1) || !listening(p2) {
		t.Fatalf("outside bare stop: %+v", r)
	}

	// from inside one worktree, only that one stops
	if r := runIn(t, bin, state, wt2, "stop", "web"); r.code != 0 {
		t.Fatalf("inside stop: %+v", r)
	}
	if listening(p2) || !listening(p1) {
		t.Fatalf("wrong server stopped: p1 up=%v p2 up=%v", listening(p1), listening(p2))
	}

	// from outside, stop by port
	if r := runIn(t, bin, state, outside, "stop", u1.out[strings.LastIndex(u1.out, ":")+1:]); r.code != 0 {
		t.Fatalf("stop by port: %+v", r)
	}
	if listening(p1) {
		t.Fatal("port stop did not stop the server")
	}

	// not found names what was searched and never calls the directory a project
	nf := runIn(t, bin, state, outside, "stop", "frontend")
	if nf.code == 0 || !strings.Contains(nf.err, "searched:") || strings.Contains(nf.err, "for "+filepath.Base(outside)) {
		t.Fatalf("not found: %+v", nf)
	}
}
