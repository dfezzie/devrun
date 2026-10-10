package proc

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

func start(t *testing.T, cmd ...string) (registry.Entry, runner.Handle) {
	t.Helper()
	e := registry.Entry{
		Kind: Kind, Cmd: cmd, Ports: map[string]int{"PORT": 41999},
		Log: filepath.Join(t.TempDir(), "logs", "x.log"),
	}
	h, err := Runner{}.Start(runner.Spec{Entry: e, Dir: t.TempDir(), Primary: "PORT"})
	if err != nil {
		t.Fatal(err)
	}
	e.PID, e.PGID, e.PIDStart = h.PID, h.PGID, h.PIDStart
	t.Cleanup(func() { syscall.Kill(-h.PGID, syscall.SIGKILL) })
	return e, h
}

// reap stops zombies from lingering: tests are the parent here, devrun's real
// parent exits and init reaps.
func reap(pid int) { go func() { var ws syscall.WaitStatus; syscall.Wait4(pid, &ws, 0, nil) }() }

func TestSubstitute(t *testing.T) {
	got := Substitute([]string{"a", "--port={port}", "{port}:{port}"}, 41234)
	if strings.Join(got, " ") != "a --port=41234 41234:41234" {
		t.Fatal(got)
	}
}

func TestStartSetsEnvPortLogAndGroup(t *testing.T) {
	e, h := start(t, "sh", "-c", `echo "env=$PORT arg=$1"`, "sh", "{port}")
	reap(h.PID)
	if h.PID != h.PGID || h.PIDStart == 0 {
		t.Fatalf("handle %+v", h)
	}
	if pgid, _ := syscall.Getpgid(h.PID); pgid != 0 && pgid != h.PGID {
		t.Fatalf("not own group: %d", pgid)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(e.Log)
		if strings.Contains(string(b), "env=41999 arg=41999") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(e.Log)
	t.Fatalf("log: %q", b)
}

func TestStartMissingBinary(t *testing.T) {
	_, err := Runner{}.Start(runner.Spec{Entry: registry.Entry{Cmd: []string{"definitely-not-a-binary-xyz"}, Log: filepath.Join(t.TempDir(), "l")}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAliveRejectsWrongStartTime(t *testing.T) {
	e, h := start(t, "sleep", "30")
	reap(h.PID)
	if !(Runner{}).Alive(e) {
		t.Fatal("should be alive")
	}
	e.PIDStart++
	if (Runner{}).Alive(e) {
		t.Fatal("recycled pid must not look alive")
	}
}

func TestStopKillsWholeGroupWithSIGTERM(t *testing.T) {
	// parent shell plus a background child, both in the group
	e, h := start(t, "sh", "-c", "sleep 60 & wait")
	reap(h.PID)
	time.Sleep(200 * time.Millisecond)
	if err := (Runner{}).Stop(e, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if !groupGone(h.PGID) {
		t.Fatal("group survived")
	}
}

func TestStopEscalatesToSIGKILL(t *testing.T) {
	e, h := start(t, "sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	reap(h.PID)
	time.Sleep(200 * time.Millisecond)
	t0 := time.Now()
	if err := (Runner{}).Stop(e, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) < 400*time.Millisecond || !groupGone(h.PGID) {
		t.Fatalf("expected escalation, took %v", time.Since(t0))
	}
}

func TestStopRefusesRecycledPID(t *testing.T) {
	e, h := start(t, "sleep", "30")
	reap(h.PID)
	e.PIDStart++ // registry says a different process owned this PID
	if err := (Runner{}).Stop(e, time.Second); err != nil {
		t.Fatal(err)
	}
	if !pidAlive(h.PID, h.PIDStart) {
		t.Fatal("killed a process it did not start")
	}
}

func TestStopAlreadyDeadIsNoop(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Run()
	e := registry.Entry{PID: cmd.Process.Pid, PGID: cmd.Process.Pid, PIDStart: 1}
	if err := (Runner{}).Stop(e, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestParseStatWithAwkwardComm(t *testing.T) {
	s := "123 (a b) c) S 1 123 123 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 1 0 98765 1 1 0"
	st, start, err := parseStat(s)
	if err != nil || st != 'S' || start != 98765 {
		t.Fatalf("%c %d %v", st, start, err)
	}
}

func TestTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644)
	var b bytes.Buffer
	if err := TailFile(context.Background(), p, 2, false, &b); err != nil || b.String() != "3\n4\n" {
		t.Fatalf("%q %v", b.String(), err)
	}
	b.Reset()
	TailFile(context.Background(), p, 0, false, &b)
	if b.String() != "1\n2\n3\n4\n" {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	TailFile(context.Background(), p, 99, false, &b)
	if b.String() != "1\n2\n3\n4\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestTailFollow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	os.WriteFile(p, []byte("a\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("b\n")
		f.Close()
	}()
	var b bytes.Buffer
	TailFile(ctx, p, 10, true, &b)
	if b.String() != "a\nb\n" {
		t.Fatalf("%q", b.String())
	}
}
