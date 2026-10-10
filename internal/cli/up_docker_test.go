package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestComposePlanPortVars(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644)
	chdir(t, dir)
	real, _ := os.Getwd()

	p, err := buildComposePlan("", nil, nil, false, nil)
	if err != nil || len(p.PortNames) != 1 || p.PortNames[0] != "PORT" || p.Primary != "PORT" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = buildComposePlan("web,api,db", []string{"web=FRONTEND_PORT"}, nil, true, []string{"--quiet-pull"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.PortNames, ",") != "FRONTEND_PORT,API_PORT,DB_PORT" || p.Primary != "FRONTEND_PORT" {
		t.Fatalf("%+v", p)
	}
	want := "docker compose -f " + filepath.Join(real, "compose.yml") + " up -d --wait --build --quiet-pull"
	if strings.Join(p.Cmd, " ") != want {
		t.Fatalf("cmd %q want %q", strings.Join(p.Cmd, " "), want)
	}
	for _, bad := range []struct {
		ports string
		pv    []string
	}{{"web", []string{"api=X"}}, {"web", []string{"web"}}, {"", []string{"web=X"}}, {"web,,api", nil}} {
		if _, err := buildComposePlan(bad.ports, bad.pv, nil, false, nil); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func TestComposeFileDiscoveryAndContainerName(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	if _, err := findComposeFile(sub); err == nil {
		t.Fatal("expected not found")
	}
	f := filepath.Join(root, "docker-compose.yml")
	os.WriteFile(f, []byte("services:\n  web:\n    # container_name: no\n    image: x\n"), 0o644)
	if got, err := findComposeFile(sub); err != nil || got != f {
		t.Fatalf("%q %v", got, err)
	}
	if hasContainerName(f) {
		t.Fatal("commented container_name must not warn")
	}
	os.WriteFile(f, []byte("services:\n  web:\n    container_name: fixed\n"), 0o644)
	if !hasContainerName(f) {
		t.Fatal("container_name not detected")
	}
}
