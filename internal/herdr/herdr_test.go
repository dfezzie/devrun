package herdr

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestInPaneNeedsPaneID(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "")
	if InPane() {
		t.Fatal("HERDR_ENV without a pane id must not count as in a pane")
	}
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	if !InPane() {
		t.Fatal("expected in pane")
	}
	t.Setenv("HERDR_ENV", "")
	if InPane() {
		t.Fatal("HERDR_ENV unset must not count as in a pane")
	}
}

func TestBinaryPrefersEnvPathThenPATH(t *testing.T) {
	installFake(t)
	b, ok := Binary()
	if !ok || !strings.HasSuffix(b, "/herdr") {
		t.Fatalf("PATH lookup: %q %v", b, ok)
	}
	t.Setenv("HERDR_BIN_PATH", b)
	if got, _ := Binary(); got != b {
		t.Fatal(got)
	}
	t.Setenv("HERDR_BIN_PATH", "/nonexistent/herdr")
	if got, ok := Binary(); !ok || got != b {
		t.Fatalf("bad HERDR_BIN_PATH should fall back to PATH, got %q", got)
	}
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("PATH", t.TempDir())
	if _, ok := Binary(); ok {
		t.Fatal("expected not found")
	}
}

func TestDetectOwnerNamedUnnamedAndOutside(t *testing.T) {
	installFake(t)
	setPane(t, "w9:p2")
	o := DetectOwner()
	if o == nil || o.Pane != "w9:p2" || o.Workspace != "w9" || o.Tab != "w9:t1" || o.Agent != "worker" {
		t.Fatalf("%+v", o)
	}
	setPane(t, "w9:p1")
	if o := DetectOwner(); o.Agent != "claude" {
		t.Fatalf("unnamed agent should fall back to kind: %+v", o)
	}
	t.Setenv("FAKE_MODE", "noserver")
	if o := DetectOwner(); o == nil || o.Pane != "w9:p1" || o.Agent != "" {
		t.Fatalf("server errors keep env ids: %+v", o)
	}
	t.Setenv("HERDR_ENV", "")
	if DetectOwner() != nil {
		t.Fatal("outside herdr must have no owner (never fall back to `pane current`)")
	}
}

func TestNotifyArgsAndResult(t *testing.T) {
	log := installFake(t)
	res, err := Notify("title", "body text", "request")
	if err != nil || !res.Shown || res.Reason != "shown" {
		t.Fatalf("%+v %v", res, err)
	}
	data, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(data)); got != "notification show title --body body text --sound request" {
		t.Fatalf("argv: %q", got)
	}
	t.Setenv("FAKE_MODE", "hidden")
	res, err = Notify("t", "", "")
	if err != nil || res.Shown || res.Reason != "disabled" {
		t.Fatalf("%+v %v", res, err)
	}
	t.Setenv("FAKE_MODE", "noserver")
	if _, err = Notify("t", "", ""); !ServerNotRunning(err) {
		t.Fatalf("want server_not_running, got %v", err)
	}
}

func TestNotifyTruncatesToHerdrLimits(t *testing.T) {
	log := installFake(t)
	Notify(strings.Repeat("t", 200), strings.Repeat("b", 500), "")
	data, _ := os.ReadFile(log)
	f := strings.Fields(string(data))
	if len([]rune(f[2])) != 80 || len([]rune(f[4])) != 240 {
		t.Fatalf("lens %d %d", len([]rune(f[2])), len([]rune(f[4])))
	}
}

func TestRunTimesOut(t *testing.T) {
	installFake(t)
	t.Setenv("FAKE_MODE", "hang")
	start := time.Now()
	_, err := Run(300*time.Millisecond, "agent", "list")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not time out promptly")
	}
}

func TestPaneExists(t *testing.T) {
	installFake(t)
	if ok, err := PaneExists("w9:p2"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := PaneExists("w9:p7"); ok || err != nil {
		t.Fatal(ok, err)
	}
	t.Setenv("FAKE_MODE", "noserver")
	if _, err := PaneExists("w9:p2"); err == nil {
		t.Fatal("server down should be an error, not 'pane gone'")
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("héllo", 5) != "héllo" || Truncate("héllo wörld", 5) != "héll…" || Truncate("abc", 1) != "a" {
		t.Fatal("truncate")
	}
}
