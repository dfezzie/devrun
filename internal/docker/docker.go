// Package docker implements the "docker" kind: compose stacks owned by devrun.
//
// Safety rules enforced here: only compose projects named devrun-* are ever
// touched, volumes are never removed, and nothing mounts the docker socket.
package docker

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
)

// Kind is the registry kind string for compose stacks.
const Kind = "docker"

// ProjectPrefix is the only compose project prefix devrun will operate on.
const ProjectPrefix = "devrun-"

var invalidProjRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// ProjectName builds COMPOSE_PROJECT_NAME: devrun-<project>-<hash of worktree>.
// Compose requires lowercase alphanumerics, dashes and underscores.
func ProjectName(project, worktree string) string {
	p := strings.Trim(invalidProjRe.ReplaceAllString(strings.ToLower(project), "-"), "-_")
	if p == "" {
		p = "x"
	}
	sum := sha1.Sum([]byte(worktree))
	return ProjectPrefix + p + "-" + hex.EncodeToString(sum[:])[:6]
}

var envNameRe = regexp.MustCompile(`[^A-Z0-9]+`)

// PortVar maps a port name to its env var: "web" -> "WEB_PORT". A name that
// already looks like an override target is not used here; see cli --port-var.
func PortVar(name string) string {
	n := strings.Trim(envNameRe.ReplaceAllString(strings.ToUpper(name), "_"), "_")
	return n + "_PORT"
}

// SafeProject refuses compose projects outside the devrun- prefix.
func SafeProject(name string) error {
	if !strings.HasPrefix(name, ProjectPrefix) || len(name) <= len(ProjectPrefix) {
		return fmt.Errorf("refusing to touch compose project %q: not a %s* project", name, ProjectPrefix)
	}
	return nil
}

// Available reports whether a docker binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// stackEnv is the environment compose needs on every invocation, so
// ${WEB_PORT}-style interpolation works for ps/down/logs as well as up.
func stackEnv(e registry.Entry) []string {
	env := os.Environ()
	names := make([]string, 0, len(e.Ports))
	for n := range e.Ports {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		env = append(env, fmt.Sprintf("%s=%d", n, e.Ports[n]))
	}
	return append(env, "COMPOSE_PROJECT_NAME="+e.ComposeProject)
}

// composeFileArgs returns the "-f file" global arguments recorded in e.Cmd
// (everything between "compose" and "up").
func composeFileArgs(cmd []string) []string {
	var out []string
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == "up" {
			break
		}
		if cmd[i] == "-f" {
			out = append(out, "-f", cmd[i+1])
			i++
		}
	}
	return out
}

// composeCmd builds `docker compose -p NAME [-f ...] <args...>` for entry e.
func composeCmd(ctx context.Context, e registry.Entry, args ...string) *exec.Cmd {
	full := []string{"compose", "-p", e.ComposeProject}
	full = append(full, composeFileArgs(e.Cmd)...)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = e.Worktree
	cmd.Env = stackEnv(e)
	return cmd
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
