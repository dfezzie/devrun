package docker

import (
	"os/exec"
	"time"

	"github.com/exterkamp/devrun/internal/alloc"
)

func init() { alloc.RegisterSource(PublishedPorts) }

// PublishedPorts returns host ports published by any running container. It is
// an alloc.Source that degrades to "nothing known" when docker is missing,
// stopped, or slow, so port allocation never fails because of docker.
func PublishedPorts() (map[int]bool, error) {
	if !Available() {
		return nil, nil
	}
	ctx, cancel := withTimeout(3 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Ports}}").Output()
	if err != nil {
		return nil, nil
	}
	return ParsePublished(string(out)), nil
}
