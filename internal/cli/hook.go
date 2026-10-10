package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	Register(Command{Name: "hook", Summary: "git integration: hook install [--force] | hook wrapper", Run: cmdHook})
}

const hookMarker = "# devrun-managed hook"

const hookScript = `#!/bin/sh
` + hookMarker + `
# Installed by "devrun hook install". Git has no hook for removing a worktree,
# so this runs gc (which stops servers whose worktree directory is gone) after
# every checkout. Remove this file to uninstall.
command -v devrun >/dev/null 2>&1 || exit 0
devrun gc --quiet >/dev/null 2>&1 || true
exit 0
`

// wrapperScript is a shell function to source from your shell profile.
const wrapperScript = `# Remove a worktree and stop its devrun servers first.
# Usage: gwt-rm <worktree-path> [git worktree remove options]
gwt-rm() {
  local wt
  wt=$(cd "$1" 2>/dev/null && pwd -P) || { echo "gwt-rm: no such directory: $1" >&2; return 1; }
  devrun stop --worktree "$wt" --yes && git worktree remove "$@"
}
`

func cmdHook(env *Env, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: devrun hook install [--force] | devrun hook wrapper")
	}
	switch args[0] {
	case "wrapper":
		fmt.Fprint(env.Stdout, wrapperScript)
		return nil
	case "install":
		fs := newFlagSet(env, "hook install")
		force := fs.Bool("force", false, "overwrite an existing post-checkout hook that devrun did not write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return env.hookInstall(*force)
	}
	return fmt.Errorf("unknown hook command %q", args[0])
}

func (env *Env) hookInstall(force bool) error {
	cmd := exec.Command("git", "rev-parse", "--git-path", "hooks")
	cmd.Dir = env.Cwd
	out, err := cmd.Output()
	if err != nil {
		return errors.New("not inside a git repository")
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(env.Cwd, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "post-checkout")
	if old, err := os.ReadFile(path); err == nil && !strings.Contains(string(old), hookMarker) && !force {
		return fmt.Errorf("%s already exists and was not written by devrun; rerun with --force to replace it, or add `devrun gc --quiet` to it yourself", path)
	}
	if err := os.WriteFile(path, []byte(hookScript), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Fprintln(env.Stderr, "devrun: installed", path)
	fmt.Fprintln(env.Stderr, "devrun: git has no worktree-removal hook; to stop servers as you remove a worktree, add this to your shell profile:")
	fmt.Fprintln(env.Stderr)
	fmt.Fprint(env.Stdout, wrapperScript)
	return nil
}
