package gitctx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestResolveMainAndLinkedWorktree(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "myrepo")
	os.Mkdir(repo, 0o755)
	run(t, repo, "init", "-q")
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, repo, "checkout", "-q", "-b", "main-x")
	wt := filepath.Join(root, "myrepo-feat")
	run(t, repo, "worktree", "add", "-q", "-b", "feat/card-art", wt)
	os.MkdirAll(filepath.Join(wt, "sub", "dir"), 0o755)

	c, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "myrepo" || c.Worktree != repo || c.Branch != "main-x" || !c.InGit {
		t.Fatalf("main: %+v", c)
	}

	c, err = Resolve(filepath.Join(wt, "sub", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "myrepo" || c.Worktree != wt || c.Branch != "feat/card-art" {
		t.Fatalf("linked: %+v", c)
	}

	run(t, wt, "checkout", "-q", "--detach")
	c, _ = Resolve(wt)
	if len(c.Branch) < len("detached@")+4 || c.Branch[:9] != "detached@" {
		t.Fatalf("detached: %+v", c)
	}
}

func TestResolveOutsideGit(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	c, err := Resolve(dir)
	if err != nil || c.InGit || c.Worktree != dir || c.Project != filepath.Base(dir) {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestProjectFromCommonDir(t *testing.T) {
	for in, want := range map[string]string{
		"/a/my-app/.git": "my-app",
		"/a/web.git":       "web",
	} {
		if got := ProjectFromCommonDir(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
