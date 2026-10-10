package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/exterkamp/devrun/internal/alloc"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

// fakeRunner proves a new kind plugs in without touching proc or cli code.
type fakeRunner struct{ alive bool }

func (fakeRunner) Kind() string                                                          { return "fake" }
func (fakeRunner) Start(runner.Spec) (runner.Handle, error)                              { return runner.Handle{}, nil }
func (f fakeRunner) Alive(registry.Entry) bool                                           { return f.alive }
func (fakeRunner) Stop(registry.Entry, time.Duration) error                              { return nil }
func (fakeRunner) Logs(context.Context, registry.Entry, runner.LogOpts, io.Writer) error { return nil }

func testEnv(t *testing.T, listening map[int]bool) (*Env, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Env{
		Stdout: &out, Stderr: &bytes.Buffer{}, Cwd: t.TempDir(),
		Store: &registry.Store{Dir: t.TempDir()}, Alloc: &alloc.Allocator{Min: 41000, Max: 41999},
		Now:       time.Now,
		Listening: func() (map[int]bool, error) { return listening, nil },
	}, &out
}

func TestLsStatesWithFakeKind(t *testing.T) {
	runner.Register(fakeRunner{alive: true})
	env, out := testEnv(t, map[int]bool{41001: true})
	env.Store.Update(func(f *registry.File) error {
		f.Upsert(&registry.Entry{Key: "p:/w:up", Project: "p", Name: "up", Kind: "fake", Ports: map[string]int{"PORT": 41001}, StartedAt: time.Now()})
		f.Upsert(&registry.Entry{Key: "p:/w:st", Project: "p", Name: "st", Kind: "fake", Ports: map[string]int{"PORT": 41002}, StartedAt: time.Now()})
		f.Upsert(&registry.Entry{Key: "p:/w:dead", Project: "p", Name: "dead", Kind: "gone", Ports: map[string]int{"PORT": 41003}, StartedAt: time.Now()})
		return nil
	})
	if code := Run(env, []string{"ls", "--json", "--all"}); code != 0 {
		t.Fatal(code)
	}
	var vs []View
	if err := json.Unmarshal(out.Bytes(), &vs); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vs {
		got[v.Name] = v.State
	}
	if got["up"] != "up" || got["st"] != "starting" || got["dead"] != "dead" {
		t.Fatalf("%v", got)
	}
	out.Reset()
	Run(env, []string{"ls", "--json"})
	if strings.Contains(out.String(), `"dead"`) {
		t.Fatal("dead shown without --all")
	}
}

func TestUnknownCommandAndNoArgs(t *testing.T) {
	env, _ := testEnv(t, nil)
	if Run(env, []string{"nope"}) != 2 || Run(env, nil) != 2 {
		t.Fatal("expected usage exit code 2")
	}
}

func TestParseInterspersed(t *testing.T) {
	env, _ := testEnv(t, nil)
	fs := newFlagSet(env, "x")
	f := fs.Bool("f", false, "")
	pos, err := parseInterspersed(fs, []string{"web", "-f"})
	if err != nil || !*f || len(pos) != 1 || pos[0] != "web" {
		t.Fatalf("%v %v %v", pos, *f, err)
	}
}
