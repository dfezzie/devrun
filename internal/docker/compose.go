package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/exterkamp/devrun/internal/hygiene"
)

func init() { hygiene.SetCompose(Detector{}) }

// Detector is the hygiene.ComposeDetector backed by the docker CLI. It only
// ever reports or touches projects named devrun-*, and never removes volumes.
type Detector struct{}

// Find lists compose projects (running or not) named devrun-* that are not in
// known. Without docker on PATH or a running daemon it finds nothing.
func (Detector) Find(known map[string]bool) ([]string, error) {
	if !Available() {
		return nil, nil
	}
	ctx, cancel := withTimeout(10 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "compose", "ls", "--all", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ls: %w", err)
	}
	var projects []struct{ Name string }
	if err := json.Unmarshal(out, &projects); err != nil {
		return nil, fmt.Errorf("parsing docker compose ls: %w", err)
	}
	var names []string
	for _, p := range projects {
		if strings.HasPrefix(p.Name, ProjectPrefix) && SafeProject(p.Name) == nil && !known[p.Name] {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Down runs `docker compose -p NAME down` (no --volumes) for a devrun-* project.
func (Detector) Down(project string) error {
	if err := SafeProject(project); err != nil {
		return err
	}
	if !Available() {
		return errors.New("docker not found on PATH")
	}
	ctx, cancel := withTimeout(2 * time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "down").CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
