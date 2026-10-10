package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEndHerdrOwnerNotifyClear(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := initRepo(t, root, "demo")
	t.Cleanup(func() { runIn(t, bin, state, repo, "stop") })

	fake := t.TempDir()
	calls := filepath.Join(fake, "calls.log")
	os.WriteFile(filepath.Join(fake, "herdr"), []byte(`#!/bin/sh
echo "$@" >> "`+calls+`"
case "$1 $2" in
"agent list") echo '{"result":{"agents":[{"agent":"claude","name":"worker","pane_id":"w9:p2"}]}}';;
"notification show") echo '{"result":{"shown":true,"reason":"shown"}}';;
*) echo '{"error":{"code":"unknown","message":"fake"}}'; exit 1;;
esac
`), 0o755)
	t.Setenv("PATH", fake+":"+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w9:p2")
	t.Setenv("HERDR_TAB_ID", "w9:t1")
	t.Setenv("HERDR_WORKSPACE_ID", "w9")

	up := runIn(t, bin, state, repo, "up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1")
	if up.code != 0 {
		t.Fatalf("up: %+v", up)
	}
	type view struct {
		Owner   map[string]string `json:"owner"`
		Flagged map[string]string `json:"flagged"`
	}
	ls := func() view {
		var vs []view
		if err := json.Unmarshal([]byte(runIn(t, bin, state, repo, "ls", "--json").out), &vs); err != nil || len(vs) != 1 {
			t.Fatalf("ls: %v", err)
		}
		return vs[0]
	}
	want := map[string]string{"herdr_pane": "w9:p2", "herdr_workspace": "w9", "herdr_tab": "w9:t1", "agent": "worker"}
	if v := ls(); len(v.Owner) != 4 || v.Owner["herdr_pane"] != want["herdr_pane"] || v.Owner["herdr_workspace"] != "w9" ||
		v.Owner["herdr_tab"] != "w9:t1" || v.Owner["agent"] != "worker" || v.Flagged != nil {
		t.Fatalf("owner: %+v", v)
	}

	if n := runIn(t, bin, state, repo, "notify", "TEST ONLY: ignore"); n.code != 0 || n.err != "" {
		t.Fatalf("notify: %+v", n)
	}
	data, _ := os.ReadFile(calls)
	if !strings.Contains(string(data), "notification show demo/demo: inspect :") || !strings.Contains(string(data), up.out+" TEST ONLY: ignore") {
		t.Fatalf("herdr calls:\n%s", data)
	}
	if v := ls(); v.Flagged["message"] != "TEST ONLY: ignore" {
		t.Fatalf("flag: %+v", v)
	}
	if w := runIn(t, bin, state, repo, "watch", "--once"); w.code != 0 || !strings.Contains(w.out, "TEST ONLY: ignore") {
		t.Fatalf("watch: %+v", w)
	}
	if c := runIn(t, bin, state, repo, "clear"); c.code != 0 {
		t.Fatalf("clear: %+v", c)
	}
	if v := ls(); v.Flagged != nil {
		t.Fatalf("still flagged: %+v", v)
	}
	if d := runIn(t, bin, state, repo, "doctor"); d.code != 0 || !strings.Contains(d.out, "state-dir") {
		t.Fatalf("doctor: %+v", d)
	}
}
