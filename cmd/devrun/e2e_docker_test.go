package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const e2eImage = "nginx:alpine" // small, already pulled; never built or pulled here

func skipUnlessDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipping docker e2e: docker not on PATH")
	}
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("skipping docker e2e: docker compose unavailable: %v %s", err, out)
	}
	if out, err := exec.Command("docker", "image", "inspect", e2eImage).CombinedOutput(); err != nil {
		t.Skipf("skipping docker e2e: image %s not present locally (devrun tests never pull): %s", e2eImage, out)
	}
}

func TestEndToEndCompose(t *testing.T) {
	skipUnlessDocker(t)
	bin := buildBinary(t)
	state := t.TempDir()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := initRepo(t, root, "dkdemo")
	compose := `services:
  web:
    image: ` + e2eImage + `
    network_mode: bridge  # no custom network: hosts can run out of address pools
    ports: ["${WEB_PORT}:80"]
    volumes: ["data:/data"]
volumes:
  data: {}
`
	if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(repo))
	project := "devrun-dkdemo-" + hex.EncodeToString(sum[:])[:6]
	t.Cleanup(func() {
		runIn(t, bin, state, repo, "stop")
		// our own throwaway project only: remove it and its volume
		exec.Command("docker", "compose", "-p", project, "-f", filepath.Join(repo, "docker-compose.yml"), "down", "-v").Run()
	})

	up := runIn(t, bin, state, repo, "up", "--compose", "--ports", "web", "--wait", "90s")
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

	// idempotent
	up2 := runIn(t, bin, state, repo, "up", "--compose", "--ports", "web")
	if up2.code != 0 || up2.out != up.out || !strings.Contains(up2.err, "already running") {
		t.Fatalf("second up: %+v", up2)
	}

	ls := runIn(t, bin, state, repo, "ls", "--json")
	var views []map[string]interface{}
	if err := json.Unmarshal([]byte(ls.out), &views); err != nil || len(views) != 1 {
		t.Fatalf("ls: %v %q", err, ls.out)
	}
	v := views[0]
	if v["kind"] != "docker" || v["state"] != "up" || v["url"] != up.out {
		t.Fatalf("view: %v", v)
	}
	if ports, _ := v["ports"].(map[string]interface{}); ports["WEB_PORT"] == nil {
		t.Fatalf("ports: %v", v["ports"])
	}

	if u := runIn(t, bin, state, repo, "url"); u.out != up.out {
		t.Fatalf("url: %+v", u)
	}
	if l := runIn(t, bin, state, repo, "logs", "-n", "20"); l.code != 0 {
		t.Fatalf("logs: %+v", l)
	}

	st := runIn(t, bin, state, repo, "stop")
	if st.code != 0 {
		t.Fatalf("stop: %+v", st)
	}
	if listening(port) {
		t.Fatal("port still listening after stop")
	}
	if out, _ := exec.Command("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+project).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("containers still running: %s", out)
	}
	// volumes survive stop
	vols, _ := exec.Command("docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+project).Output()
	if strings.TrimSpace(string(vols)) == "" {
		t.Fatal("stop removed the volume; it must keep volumes")
	}
	if ls := runIn(t, bin, state, repo, "ls", "--json", "--all"); ls.out != "[]" {
		t.Fatalf("registry not cleared: %q", ls.out)
	}
}
