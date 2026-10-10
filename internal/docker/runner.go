package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/exterkamp/devrun/internal/proc"
	"github.com/exterkamp/devrun/internal/registry"
	"github.com/exterkamp/devrun/internal/runner"
)

func init() { runner.Register(Runner{}) }

// Runner implements runner.Runner for compose stacks.
//
// Start must not hold the registry lock for a slow `compose up --wait`, so it
// launches the compose command detached (setsid, output to the entry's log)
// and returns. The Handle carries the compose project; PID/PIDStart describe
// the short-lived launcher. Alive is true while the launcher runs or any
// container of the project is running.
type Runner struct{}

func (Runner) Kind() string { return Kind }

func (Runner) Start(spec runner.Spec) (runner.Handle, error) {
	e := spec.Entry
	if len(e.Cmd) < 3 || e.Cmd[0] != "docker" || e.Cmd[1] != "compose" {
		return runner.Handle{}, errors.New("docker kind: cmd must start with `docker compose`")
	}
	e.ComposeProject = ProjectName(e.Project, e.Worktree)
	if err := SafeProject(e.ComposeProject); err != nil {
		return runner.Handle{}, err
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return runner.Handle{}, errors.New("docker not found on PATH")
	}
	args := append([]string{"compose", "-p", e.ComposeProject}, e.Cmd[2:]...)
	if err := os.MkdirAll(filepath.Dir(e.Log), 0o755); err != nil {
		return runner.Handle{}, err
	}
	logf, err := os.OpenFile(e.Log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return runner.Handle{}, err
	}
	defer logf.Close()
	cmd := exec.Command(docker, args...)
	cmd.Dir = spec.Dir
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = stackEnv(e)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return runner.Handle{}, err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	start, _ := proc.StartTime(pid)
	return runner.Handle{PID: pid, PGID: pid, PIDStart: start, ComposeProject: e.ComposeProject}, nil
}

func (Runner) Alive(e registry.Entry) bool {
	if e.PID > 0 && proc.PIDAlive(e.PID, e.PIDStart) {
		return true // `compose up --wait` still in progress
	}
	if SafeProject(e.ComposeProject) != nil || !Available() {
		return false
	}
	ctx, cancel := withTimeout(10 * time.Second)
	defer cancel()
	out, err := composeCmd(ctx, e, "ps", "--format", "json").Output()
	if err != nil {
		return false
	}
	cs, err := ParseComposePS(out)
	return err == nil && Running(cs)
}

// Stop runs `docker compose -p NAME down`. It never passes --volumes.
func (Runner) Stop(e registry.Entry, grace time.Duration) error {
	if err := SafeProject(e.ComposeProject); err != nil {
		return err
	}
	// An unfinished `up` would fight the teardown; the proc runner's guarded
	// stop kills only our own launcher.
	if e.PID > 0 && proc.PIDAlive(e.PID, e.PIDStart) {
		if err := (proc.Runner{}).Stop(e, grace); err != nil {
			return err
		}
	}
	if !Available() {
		return errors.New("docker not found on PATH")
	}
	timeout := int(grace / time.Second)
	if timeout < 1 {
		timeout = 1
	}
	ctx, cancel := withTimeout(2 * time.Minute)
	defer cancel()
	out, err := composeCmd(ctx, e, "down", "--timeout", strconv.Itoa(timeout)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down: %v: %s", err, out)
	}
	return nil
}

func (Runner) Logs(ctx context.Context, e registry.Entry, opts runner.LogOpts, out io.Writer) error {
	if err := SafeProject(e.ComposeProject); err != nil {
		return err
	}
	tail := "all"
	if opts.Lines > 0 {
		tail = strconv.Itoa(opts.Lines)
	}
	args := []string{"logs", "--no-color", "--tail", tail}
	if opts.Follow {
		args = append(args, "--follow")
	}
	cmd := composeCmd(ctx, e, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
