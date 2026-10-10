// Package gitctx resolves project / worktree / branch from a directory.
package gitctx

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Context identifies where an agent is working.
type Context struct {
	Project  string // name of the repository (shared by all its worktrees)
	Worktree string // absolute path of this worktree's root
	Branch   string // branch name, or "detached@<sha>"; empty outside git
	InGit    bool
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Resolve inspects dir. Inside a git checkout (main or linked worktree) the
// project is the main repository's name, so every worktree of one repo shares
// a project. Outside git it falls back to the directory itself.
func Resolve(dir string) (Context, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Context{}, err
	}
	top, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return Context{Project: filepath.Base(abs), Worktree: abs}, nil
	}
	c := Context{Worktree: filepath.Clean(top), InGit: true}

	// --git-common-dir is the main repo's .git even from a linked worktree.
	// (--path-format=absolute needs git 2.31, so resolve by hand.)
	common, err := git(abs, "rev-parse", "--git-common-dir")
	if err != nil {
		return Context{}, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(abs, common)
	}
	c.Project = ProjectFromCommonDir(filepath.Clean(common))

	if b, err := git(abs, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && b != "" {
		c.Branch = b
	} else if sha, err := git(abs, "rev-parse", "--short", "HEAD"); err == nil {
		c.Branch = "detached@" + sha
	}
	return c, nil
}

// ProjectFromCommonDir names the project for a git common dir: "/x/repo/.git"
// is "repo"; a bare "/x/repo.git" is "repo".
func ProjectFromCommonDir(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common))
	}
	return strings.TrimSuffix(filepath.Base(common), ".git")
}
