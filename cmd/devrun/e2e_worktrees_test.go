package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestEndToEndTwoWorktrees is the agent workflow: two worktrees of one repo,
// each gets its own stable port, is listed with its branch, can be flagged for
// inspection, and is cleaned up by stop --project.
func TestEndToEndTwoWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping the two-worktree end-to-end test")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())

	main := filepath.Join(root, "shop")
	os.MkdirAll(main, 0o755)
	gitIn(t, main, "init", "-q")
	os.WriteFile(filepath.Join(main, "README"), []byte("x"), 0o644)
	gitIn(t, main, "add", ".")
	gitIn(t, main, "commit", "-q", "-m", "init")
	gitIn(t, main, "branch", "-M", "main")
	wt2 := filepath.Join(root, "shop-feature")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat/card", wt2)
	t.Cleanup(func() { runIn(t, bin, state, main, "stop", "--project", "shop", "--yes") })

	// fake herdr so no real notification is sent
	fake := t.TempDir()
	calls := filepath.Join(fake, "calls.log")
	os.WriteFile(filepath.Join(fake, "herdr"), []byte("#!/bin/sh\necho \"$@\" >> \""+calls+"\"\n"+
		"echo '{\"result\":{\"shown\":true,\"reason\":\"shown\"}}'\n"), 0o755)
	t.Setenv("PATH", fake+":"+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w9:p2")

	srv := []string{"up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1"}
	dirs := []string{main, wt2}
	ports := make([]int, 2)
	for i, d := range dirs {
		up := runIn(t, bin, state, d, srv...)
		if up.code != 0 {
			t.Fatalf("up in %s: %+v", d, up)
		}
		ports[i] = portOf(t, up.out)
		if ports[i] < 41000 || ports[i] > 41999 {
			t.Fatalf("port %d outside 41000-41999", ports[i])
		}
	}
	if ports[0] == ports[1] {
		t.Fatalf("both worktrees got port %d", ports[0])
	}

	// stable across stop and up
	for i, d := range dirs {
		if st := runIn(t, bin, state, d, "stop"); st.code != 0 {
			t.Fatalf("stop: %+v", st)
		}
		if listening(ports[i]) {
			t.Fatalf("port %d still listening after stop", ports[i])
		}
		up := runIn(t, bin, state, d, srv...)
		if up.code != 0 || portOf(t, up.out) != ports[i] {
			t.Fatalf("port not stable in %s: %+v want %d", d, up, ports[i])
		}
	}

	// ls --json lists both with the right worktree and branch
	type view struct {
		Worktree string            `json:"worktree"`
		Branch   string            `json:"branch"`
		Port     int               `json:"port"`
		Flagged  map[string]string `json:"flagged"`
	}
	ls := func() map[string]view {
		var vs []view
		if err := json.Unmarshal([]byte(runIn(t, bin, state, main, "ls", "--project", "shop", "--json").out), &vs); err != nil {
			t.Fatal(err)
		}
		m := map[string]view{}
		for _, v := range vs {
			m[v.Worktree] = v
		}
		return m
	}
	got := ls()
	if len(got) != 2 || got[main].Branch != "main" || got[main].Port != ports[0] ||
		got[wt2].Branch != "feat/card" || got[wt2].Port != ports[1] {
		t.Fatalf("ls: %+v", got)
	}

	// notify flags exactly one; clear unflags it
	if n := runIn(t, bin, state, wt2, "notify", "TEST ONLY: ignore"); n.code != 0 {
		t.Fatalf("notify: %+v", n)
	}
	if data, _ := os.ReadFile(calls); !strings.Contains(string(data), "notification show") {
		t.Fatalf("fake herdr not called: %q", data)
	}
	got = ls()
	if got[wt2].Flagged["message"] != "TEST ONLY: ignore" || got[main].Flagged != nil {
		t.Fatalf("flags after notify: %+v", got)
	}
	if c := runIn(t, bin, state, wt2, "clear"); c.code != 0 {
		t.Fatalf("clear: %+v", c)
	}
	if got = ls(); got[wt2].Flagged != nil || got[main].Flagged != nil {
		t.Fatalf("flags after clear: %+v", got)
	}

	// stop --project cleans up both and frees the ports
	if st := runIn(t, bin, state, main, "stop", "--project", "shop", "--yes"); st.code != 0 {
		t.Fatalf("stop --project: %+v", st)
	}
	for _, p := range ports {
		if listening(p) {
			t.Fatalf("port %d still listening", p)
		}
	}
	if out := runIn(t, bin, state, main, "ls", "--json", "--all").out; out != "[]" {
		t.Fatalf("registry not empty: %s", out)
	}
}
