package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const lsJSON = `[{"Name":"devrun-b-111111","Status":"running(1)"},{"Name":"devrun-a-222222","Status":"exited(1)"},
{"Name":"devrun-known-333333","Status":"running(1)"},{"Name":"paperless","Status":"running(3)"},{"Name":"devrunner","Status":"running(1)"}]`

func TestDetectorFindsOnlyUnknownDevrunProjects(t *testing.T) {
	dir, calls := fakeDocker(t)
	os.WriteFile(filepath.Join(dir, "ls.json"), []byte(lsJSON), 0o644)
	// extend the fake: answer `compose ls`
	script, _ := os.ReadFile(filepath.Join(dir, "docker"))
	patched := strings.Replace(string(script), `*" ps "*)`, `"compose ls "*) cat "$d/ls.json"; exit 0 ;;
    *" ps "*)`, 1)
	os.WriteFile(filepath.Join(dir, "docker"), []byte(patched), 0o755)

	got, err := Detector{}.Find(map[string]bool{"devrun-known-333333": true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"devrun-a-222222", "devrun-b-111111"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("found %v want %v (calls %v)", got, want, calls())
	}
}

func TestDetectorDownIsPrefixGuardedAndKeepsVolumes(t *testing.T) {
	_, calls := fakeDocker(t)
	for _, bad := range []string{"paperless", "devrun-", "other-devrun-x", ""} {
		if err := (Detector{}).Down(bad); err == nil {
			t.Errorf("Down(%q) should be refused", bad)
		}
	}
	if c := calls(); len(c) > 1 || (len(c) == 1 && c[0] != "") {
		t.Fatalf("docker invoked for refused projects: %v", c)
	}
	if err := (Detector{}).Down("devrun-a-222222"); err != nil {
		t.Fatal(err)
	}
	c := calls()
	last := c[len(c)-1]
	if !strings.HasPrefix(last, "compose -p devrun-a-222222 down ") || strings.Contains(last, "-v") || strings.Contains(last, "volumes") {
		t.Fatalf("call %q", last)
	}
}

func TestDetectorWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got, err := (Detector{}).Find(nil); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
}
