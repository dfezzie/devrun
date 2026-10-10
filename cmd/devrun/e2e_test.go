package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type result struct {
	out, err string
	code     int
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "devrun")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func runIn(t *testing.T, bin, state, dir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DEVRUN_STATE_DIR="+state)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{strings.TrimSpace(o.String()), strings.TrimSpace(e.String()), code}
}

func portOf(t *testing.T, url string) int {
	var p int
	if _, err := fmt.Sscanf(url, "http://127.0.0.1:%d", &p); err != nil {
		t.Fatalf("bad url %q", url)
	}
	return p
}

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err == nil {
		c.Close()
	}
	return err == nil
}

func initRepo(t *testing.T, root, name string) string {
	dir := filepath.Join(root, name)
	os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return dir
}

func TestEndToEndUpLsStop(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := initRepo(t, root, "demo")
	t.Cleanup(func() { runIn(t, bin, state, repo, "stop") })

	up := runIn(t, bin, state, repo, "up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1")
	if up.code != 0 {
		t.Fatalf("up failed: %+v", up)
	}
	port := portOf(t, up.out)
	if port < 41000 || port > 41999 {
		t.Fatalf("port %d outside range", port)
	}
	resp, err := http.Get(up.out)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// idempotent: same URL, no second server
	up2 := runIn(t, bin, state, repo, "up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1")
	if up2.code != 0 || up2.out != up.out || !strings.Contains(up2.err, "already running") {
		t.Fatalf("second up: %+v (first %+v)", up2, up)
	}

	// ls --json
	ls := runIn(t, bin, state, repo, "ls", "--json")
	var views []map[string]interface{}
	if err := json.Unmarshal([]byte(ls.out), &views); err != nil || len(views) != 1 {
		t.Fatalf("ls json: %v %q", err, ls.out)
	}
	v := views[0]
	if v["project"] != "demo" || v["name"] != "web" || v["kind"] != "proc" || v["state"] != "up" ||
		v["url"] != up.out || int(v["port"].(float64)) != port || v["worktree"] != repo {
		t.Fatalf("view: %v", v)
	}
	if ls2 := runIn(t, bin, state, repo, "ls"); !strings.Contains(ls2.out, up.out) {
		t.Fatalf("ls table lacks url: %q", ls2.out)
	}
	if ls3 := runIn(t, bin, state, repo, "ls", "--project", "other", "--json"); ls3.out != "[]" {
		t.Fatalf("project filter: %q", ls3.out)
	}

	// url / url --json
	if u := runIn(t, bin, state, repo, "url"); u.out != up.out {
		t.Fatalf("url: %+v", u)
	}
	var uj map[string]interface{}
	if err := json.Unmarshal([]byte(runIn(t, bin, state, repo, "url", "--json").out), &uj); err != nil || uj["url"] != up.out {
		t.Fatalf("url json: %v %v", err, uj)
	}

	// logs: http.server logs each request to stderr
	time.Sleep(200 * time.Millisecond)
	if l := runIn(t, bin, state, repo, "logs"); !strings.Contains(l.out, "GET /") {
		t.Fatalf("logs: %+v", l)
	}

	// stop frees the port and clears the registry
	st := runIn(t, bin, state, repo, "stop")
	if st.code != 0 {
		t.Fatalf("stop: %+v", st)
	}
	if listening(port) {
		t.Fatal("port still listening after stop")
	}
	if ls := runIn(t, bin, state, repo, "ls", "--json", "--all"); ls.out != "[]" {
		t.Fatalf("registry not cleared: %q", ls.out)
	}
	if u := runIn(t, bin, state, repo, "url"); u.code == 0 {
		t.Fatal("url should fail after stop")
	}

	// same worktree gets the same port again
	up3 := runIn(t, bin, state, repo, "up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1")
	if up3.code != 0 || up3.out != up.out {
		t.Fatalf("port not stable: %+v vs %s", up3, up.out)
	}
}

func TestEndToEndDistinctWorktreesAndFailure(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a, b := initRepo(t, root, "a"), initRepo(t, root, "b")
	t.Cleanup(func() { runIn(t, bin, state, a, "stop"); runIn(t, bin, state, b, "stop") })

	srv := []string{"up", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1"}
	ua, ub := runIn(t, bin, state, a, srv...), runIn(t, bin, state, b, srv...)
	if ua.code != 0 || ub.code != 0 || ua.out == ub.out {
		t.Fatalf("a=%+v b=%+v", ua, ub)
	}
	if ls := runIn(t, bin, state, a, "ls", "--json"); strings.Count(ls.out, `"state": "up"`) != 2 {
		t.Fatalf("expected two up servers: %s", ls.out)
	}

	// a process that dies immediately fails `up` and leaves nothing registered
	bad := runIn(t, bin, state, a, "up", "--name", "bad", "--", "sh", "-c", "echo boom >&2; exit 3")
	if bad.code == 0 || !strings.Contains(bad.err, "boom") {
		t.Fatalf("bad up: %+v", bad)
	}
	if ls := runIn(t, bin, state, a, "ls", "--json", "--all"); strings.Contains(ls.out, `"bad"`) {
		t.Fatalf("failed server left registered: %s", ls.out)
	}

	// never listens: times out, non-zero, process cleaned up
	slow := runIn(t, bin, state, a, "up", "--name", "slow", "--wait", "1s", "--", "sleep", "30")
	if slow.code == 0 || !strings.Contains(slow.err, "did not listen") {
		t.Fatalf("slow up: %+v", slow)
	}

	// naming is required once there are several servers in a worktree
	two := runIn(t, bin, state, a, "up", "--name", "api", "--", "python3", "-u", "-m", "http.server", "{port}", "--bind", "127.0.0.1")
	if two.code != 0 {
		t.Fatalf("%+v", two)
	}
	if u := runIn(t, bin, state, a, "url"); u.code == 0 || !strings.Contains(u.err, "pass a name") {
		t.Fatalf("ambiguous url: %+v", u)
	}
	if u := runIn(t, bin, state, a, "url", "api"); u.out != two.out {
		t.Fatalf("url api: %+v", u)
	}
	runIn(t, bin, state, a, "stop", "api")
	if u := runIn(t, bin, state, a, "url"); u.out != ua.out {
		t.Fatalf("remaining url: %+v", u)
	}
}
