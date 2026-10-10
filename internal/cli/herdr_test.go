package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// installFakeHerdr puts a fake `herdr` first on PATH; the returned path records argv.
func installFakeHerdr(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1" in --version) echo "herdr 0.0.0-fake"; exit 0;; esac
if [ "$FAKE_MODE" = noserver ]; then
  echo '{"id":"x","error":{"code":"server_not_running","message":"no herdr server is running"}}'; exit 1
fi
case "$1 $2" in
"notification show")
  if [ "$FAKE_MODE" = hidden ]; then echo '{"id":"x","result":{"reason":"disabled","shown":false,"type":"notification_show"}}'
  else echo '{"id":"x","result":{"reason":"shown","shown":true,"type":"notification_show"}}'; fi;;
"agent list") echo '{"id":"x","result":{"agents":[{"agent":"claude","name":"worker","pane_id":"w9:p2"}],"type":"agent_list"}}';;
"api snapshot") echo '{"id":"x","result":{"snapshot":{"version":"9.9.9","protocol":22},"type":"api_snapshot"}}';;
*) echo '{"id":"x","error":{"code":"unknown","message":"fake"}}'; exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("FAKE_MODE", "")
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_TAB_ID", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	return log
}

func inPane(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w9:p2")
	t.Setenv("HERDR_TAB_ID", "w9:t1")
	t.Setenv("HERDR_WORKSPACE_ID", "w9")
}

func calls(t *testing.T, log string) string {
	data, _ := os.ReadFile(log)
	return string(data)
}

// addServer registers a fake-kind server in env's cwd as project/worktree.
func addServer(t *testing.T, env *Env, name string, port int) string {
	t.Helper()
	runner.Register(fakeRunner{alive: true})
	project := filepath.Base(env.Cwd)
	key := registry.Key(project, env.Cwd, name)
	err := env.Store.Update(func(f *registry.File) error {
		f.Upsert(&registry.Entry{Key: key, Project: project, Worktree: env.Cwd, Branch: "b", Name: name,
			Kind: "fake", Ports: map[string]int{"PORT": port}, StartedAt: time.Now().Add(-time.Minute)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func entryOf(t *testing.T, env *Env, key string) *registry.Entry {
	f, err := env.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return f.Find(key)
}

func TestDetectOwnerFromEnvAndAgentName(t *testing.T) {
	installFakeHerdr(t)
	if detectOwner() != nil {
		t.Fatal("no owner outside herdr")
	}
	inPane(t)
	o := detectOwner()
	if o == nil || *o != (registry.Owner{HerdrPane: "w9:p2", HerdrWorkspace: "w9", HerdrTab: "w9:t1", Agent: "worker"}) {
		t.Fatalf("%+v", o)
	}
}

func TestNotifyInHerdr(t *testing.T) {
	log := installFakeHerdr(t)
	inPane(t)
	env, out := testEnv(t, map[int]bool{41001: true})
	key := addServer(t, env, "web", 41001)

	if code := Run(env, []string{"notify", "check the deck layout", "--json"}); code != 0 {
		t.Fatalf("exit %d: %s", code, env.Stderr)
	}
	got := calls(t, log)
	project := filepath.Base(env.Cwd)
	for _, want := range []string{"notification show", project + "/" + project + ": inspect :41001",
		"--body http://127.0.0.1:41001 check the deck layout", "--sound request"} {
		if !strings.Contains(got, want) {
			t.Errorf("herdr argv %q missing %q", got, want)
		}
	}
	e := entryOf(t, env, key)
	if e.Flagged == nil || e.Flagged.Message != "check the deck layout" {
		t.Fatalf("flag not recorded: %+v", e.Flagged)
	}
	var r map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r["notified"] != true || r["flagged"] != true {
		t.Fatalf("%s %v", out, err)
	}
	if env.Stderr.(*bytes.Buffer).Len() != 0 {
		t.Fatalf("no stderr noise on success: %s", env.Stderr)
	}
}

func TestNotifyFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T)
		reason string
		called bool
	}{
		{"outside herdr", func(t *testing.T) {}, "not running inside herdr", false},
		{"env set but no pane id", func(t *testing.T) { t.Setenv("HERDR_ENV", "1") }, "not running inside herdr", false},
		{"herdr absent", func(t *testing.T) { inPane(t); t.Setenv("PATH", t.TempDir()) }, "herdr not found", false},
		{"no server", func(t *testing.T) { inPane(t); t.Setenv("FAKE_MODE", "noserver") }, "server_not_running", true},
		{"not shown", func(t *testing.T) { inPane(t); t.Setenv("FAKE_MODE", "hidden") }, "disabled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := installFakeHerdr(t)
			tc.setup(t)
			env, out := testEnv(t, nil)
			key := addServer(t, env, "web", 41001)
			if code := Run(env, []string{"notify", "web", "look at this", "--json"}); code != 0 {
				t.Fatalf("fallback must exit 0, got %d: %s", code, env.Stderr)
			}
			if e := entryOf(t, env, key); e.Flagged == nil {
				t.Fatal("flag must be recorded in the fallback")
			}
			se := env.Stderr.(*bytes.Buffer).String()
			if !strings.Contains(se, "NEEDS INSPECTION") || !strings.Contains(se, "http://127.0.0.1:41001 look at this") || !strings.Contains(se, tc.reason) {
				t.Fatalf("stderr: %q", se)
			}
			if strings.Contains(out.String(), `"notified":true`) {
				t.Fatal(out.String())
			}
			if (calls(t, log) != "") != tc.called {
				t.Fatalf("herdr called = %v, want %v", calls(t, log) != "", tc.called)
			}
		})
	}
}

func TestNotifyArgErrors(t *testing.T) {
	installFakeHerdr(t)
	env, _ := testEnv(t, nil)
	addServer(t, env, "web", 41001)
	addServer(t, env, "api", 41002)
	if Run(env, []string{"notify"}) == 0 || Run(env, []string{"notify", "a", "b", "c"}) == 0 {
		t.Fatal("bad arity should fail")
	}
	if Run(env, []string{"notify", "  "}) == 0 {
		t.Fatal("empty message should fail")
	}
	if Run(env, []string{"notify", "hi"}) == 0 {
		t.Fatal("two servers and no name should be ambiguous")
	}
	if Run(env, []string{"notify", "nope", "hi"}) == 0 {
		t.Fatal("unknown name should fail")
	}
	if Run(env, []string{"notify", "api", "hi"}) != 0 {
		t.Fatal(env.Stderr)
	}
}

func TestClear(t *testing.T) {
	installFakeHerdr(t)
	env, _ := testEnv(t, nil)
	key := addServer(t, env, "web", 41001)
	Run(env, []string{"notify", "x"})
	if entryOf(t, env, key).Flagged == nil {
		t.Fatal("not flagged")
	}
	if Run(env, []string{"clear"}) != 0 || entryOf(t, env, key).Flagged != nil {
		t.Fatal("flag not cleared")
	}
	env.Stderr.(*bytes.Buffer).Reset()
	if Run(env, []string{"clear", "web"}) != 0 || !strings.Contains(env.Stderr.(*bytes.Buffer).String(), "was not flagged") {
		t.Fatal("clearing an unflagged server should say so and succeed")
	}
}

func TestLsMarksFlaggedAndKeepsJSON(t *testing.T) {
	installFakeHerdr(t)
	env, out := testEnv(t, map[int]bool{41001: true})
	addServer(t, env, "web", 41001)
	Run(env, []string{"notify", "look"})
	Run(env, []string{"ls"})
	if !strings.Contains(out.String(), "up !") || strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("plain ls should mark ! without escapes:\n%q", out.String())
	}
	out.Reset()
	Run(env, []string{"ls", "--json"})
	var vs []View
	if err := json.Unmarshal(out.Bytes(), &vs); err != nil || vs[0].Flagged == nil || vs[0].Flagged.Message != "look" {
		t.Fatalf("%s %v", out, err)
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func visible(s string) string { return ansiRe.ReplaceAllString(s, "") }

func sampleViews() []View {
	now := time.Now()
	return []View{
		{Key: "p:/w/a:web", Project: "my-app", Worktree: "/home/user/code/my-app-feature", Name: "web", Port: 41237, State: StateUp,
			URL: "http://127.0.0.1:41237", StartedAt: now.Add(-3 * time.Minute),
			Owner:   nil,
			Flagged: &registry.Flag{At: now, Message: "check the new deck layout and the very long explanation that follows it"}},
		{Key: "p:/w/a:api", Project: "my-app", Worktree: "/home/user/code/my-app-feature", Name: "api", Port: 41238, State: StateStarting,
			URL: "http://127.0.0.1:41238", StartedAt: now.Add(-time.Hour),
			Owner: &registry.Owner{HerdrPane: "w3:p3", Agent: "herdr-notes"}},
	}
}

func TestRenderFrameFitsEveryWidth(t *testing.T) {
	for _, color := range []bool{false, true} {
		for _, w := range []int{1, 20, 24, 40, 60, 79, 99, 100, 140, 300} {
			lines := RenderFrame(sampleViews(), RenderOpts{Width: w, Color: color, Now: time.Now(), NewKeys: map[string]bool{"p:/w/a:web": true}})
			min := w
			if min < 20 {
				min = 20
			}
			for _, l := range lines {
				if n := len([]rune(visible(l))); n > min {
					t.Errorf("width %d color %v: line is %d cells: %q", w, color, n, visible(l))
				}
				if strings.Contains(l, "\n") {
					t.Errorf("newline inside a line: %q", l)
				}
			}
		}
	}
}

func TestRenderFrameContent(t *testing.T) {
	wide := strings.Join(RenderFrame(sampleViews(), RenderOpts{Width: 140, Now: time.Now(), NewKeys: map[string]bool{"p:/w/a:web": true}}), "\n")
	for _, want := range []string{"PROJECT", "41237", "NEW !", "check the new deck layout", "herdr-notes@w3:p3", "1 flagged", "http://127.0.0.1:41238"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide frame missing %q:\n%s", want, wide)
		}
	}
	narrow := strings.Join(RenderFrame(sampleViews(), RenderOpts{Width: 40, Now: time.Now()}), "\n")
	if !strings.Contains(narrow, "● web :41237 up !") || !strings.Contains(narrow, "○ api :41238 starting") {
		t.Errorf("narrow frame:\n%s", narrow)
	}
	if strings.Contains(narrow, "\x1b[") {
		t.Error("no escapes when color is off")
	}
	colored := strings.Join(RenderFrame(sampleViews(), RenderOpts{Width: 140, Color: true, Now: time.Now(), NewKeys: map[string]bool{"p:/w/a:web": true}}), "\n")
	if !strings.Contains(colored, ansiRev) {
		t.Error("NEW entries should be reverse video")
	}
	if empty := RenderFrame(nil, RenderOpts{Width: 40, Project: "p"}); !strings.Contains(strings.Join(empty, "\n"), "no servers running") {
		t.Errorf("%v", empty)
	}
}

func TestWatchOncePrintsPlainFrame(t *testing.T) {
	installFakeHerdr(t)
	env, out := testEnv(t, map[int]bool{41001: true})
	addServer(t, env, "web", 41001)
	Run(env, []string{"notify", "look"})
	if code := Run(env, []string{"watch", "--once"}); code != 0 {
		t.Fatal(env.Stderr)
	}
	s := out.String()
	if strings.Contains(s, "\x1b[") || !strings.Contains(s, "41001") || !strings.Contains(s, "look") {
		t.Fatalf("%q", s)
	}
}

func TestWatchLoopRedrawsWithoutClearingAndMarksNew(t *testing.T) {
	installFakeHerdr(t)
	t.Setenv("NO_COLOR", "")
	env, _ := testEnv(t, map[int]bool{41001: true})
	var buf syncBuf
	env.Stdout = &buf
	key := addServer(t, env, "web", 41001)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, env, "", 20*time.Millisecond, false, true) }()

	time.Sleep(100 * time.Millisecond)
	// Flag while the view is open: it must show up as NEW.
	env.Store.Update(func(f *registry.File) error {
		f.Find(key).Flagged = &registry.Flag{At: time.Now(), Message: "late flag"}
		return nil
	})
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "NEW !") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, altScreenOn+hideCursor) || !strings.HasSuffix(s, showCursor+altScreenOff) {
		t.Fatalf("must enter and always leave the alt screen:\n%q", s)
	}
	if strings.Contains(s, "\x1b[2J") {
		t.Fatal("full-screen clear causes flicker")
	}
	if !strings.Contains(s, cursorHome) || !strings.Contains(s, clearToEOL) || !strings.Contains(s, "late flag") || !strings.Contains(s, "NEW !") {
		t.Fatalf("%q", s)
	}
	// An unchanged registry must not be redrawn on every tick.
	if n := strings.Count(s, cursorHome); n > 8 {
		t.Errorf("%d redraws; unchanged frames should be skipped", n)
	}
}

func TestWatchAlreadyFlaggedIsNotNew(t *testing.T) {
	installFakeHerdr(t)
	env, _ := testEnv(t, map[int]bool{41001: true})
	var buf syncBuf
	env.Stdout = &buf
	addServer(t, env, "web", 41001)
	Run(env, []string{"notify", "old news"})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := watchLoop(ctx, env, "", 20*time.Millisecond, false, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "NEW") || !strings.Contains(buf.String(), "old news") {
		t.Fatalf("%q", buf.String())
	}
}

func TestWatchProjectFilter(t *testing.T) {
	env, out := testEnv(t, map[int]bool{41001: true, 41002: true})
	addServer(t, env, "web", 41001)
	env.Store.Update(func(f *registry.File) error {
		f.Upsert(&registry.Entry{Key: "other:/o:web", Project: "other", Worktree: "/o", Name: "web", Kind: "fake",
			Ports: map[string]int{"PORT": 41002}, StartedAt: time.Now()})
		return nil
	})
	Run(env, []string{"watch", "--once", "--project", "other"})
	if !strings.Contains(out.String(), "41002") || strings.Contains(out.String(), "41001") {
		t.Fatalf("%s", out)
	}
}

func TestDoctor(t *testing.T) {
	installFakeHerdr(t)
	inPane(t)
	env, out := testEnv(t, map[int]bool{41500: true})
	if code := Run(env, []string{"doctor"}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	s := out.String()
	for _, want := range []string{"herdr 0.0.0-fake", "version 9.9.9, protocol 22", "pane w9:p2", "writable", "41000-41999: 1 port(s) in use by non-devrun"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

func TestDoctorWithoutHerdrIsAWarningNotAFailure(t *testing.T) {
	installFakeHerdr(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	env, out := testEnv(t, nil)
	env.Stdout = out
	if code := Run(env, []string{"doctor", "--json"}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var cs []Check
	if err := json.Unmarshal(out.Bytes(), &cs); err != nil {
		t.Fatal(err)
	}
	levels := map[string]string{}
	for _, c := range cs {
		levels[c.Name] = c.Level
	}
	if levels["herdr"] != levelWarn || levels["herdr-pane"] != levelWarn || levels["state-dir"] != levelOK {
		t.Fatalf("%v", levels)
	}
}

func TestDoctorFailsOnUnwritableStateDir(t *testing.T) {
	installFakeHerdr(t)
	env, out := testEnv(t, nil)
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	env.Store.Dir = filepath.Join(f, "sub") // parent is a file
	if code := Run(env, []string{"doctor"}); code != 1 || !strings.Contains(out.String(), "FAIL") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// syncBuf is a bytes.Buffer safe for the watch goroutine plus the test reader.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
