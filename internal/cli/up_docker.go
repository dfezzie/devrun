package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/exterkamp/devrun/internal/docker"
)

func init() {
	RegisterUpMode(UpMode{Name: "docker", Bind: bindCompose})
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

func bindCompose(fs *flag.FlagSet) func([]string) (*UpPlan, error) {
	compose := fs.Bool("compose", false, "docker mode: run `docker compose up -d --wait` with allocated ports")
	ports := fs.String("ports", "", "docker mode: comma-separated port names (default: one PORT)")
	build := fs.Bool("build", false, "docker mode: pass --build to compose up")
	var portVars, files multiFlag
	fs.Var(&portVars, "port-var", "docker mode: override a port's env var, name=VAR (repeatable)")
	fs.Var(&files, "f", "docker mode: compose file (repeatable)")
	return func(rest []string) (*UpPlan, error) {
		if !*compose {
			return nil, nil
		}
		return buildComposePlan(*ports, portVars, files, *build, rest)
	}
}

func buildComposePlan(ports string, portVars, files []string, build bool, extra []string) (*UpPlan, error) {
	overrides := map[string]string{}
	for _, pv := range portVars {
		i := strings.IndexByte(pv, '=')
		if i <= 0 || i == len(pv)-1 {
			return nil, fmt.Errorf("bad --port-var %q: want name=VAR", pv)
		}
		overrides[strings.ToLower(pv[:i])] = pv[i+1:]
	}
	var vars []string
	if ports == "" {
		vars = []string{"PORT"}
		for n := range overrides {
			return nil, fmt.Errorf("--port-var %s given without --ports", n)
		}
	} else {
		seen := map[string]bool{}
		for _, n := range strings.Split(ports, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				return nil, errors.New("empty name in --ports")
			}
			v := docker.PortVar(n)
			if o, ok := overrides[strings.ToLower(n)]; ok {
				v = o
				delete(overrides, strings.ToLower(n))
			}
			if seen[v] {
				return nil, fmt.Errorf("duplicate port variable %s", v)
			}
			seen[v] = true
			vars = append(vars, v)
		}
		for n := range overrides {
			return nil, fmt.Errorf("--port-var names %q, which is not in --ports", n)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	var abs []string
	if len(files) == 0 {
		f, err := findComposeFile(cwd)
		if err != nil {
			return nil, err
		}
		abs = []string{f}
	} else {
		for _, f := range files {
			if !filepath.IsAbs(f) {
				f = filepath.Join(cwd, f)
			}
			if _, err := os.Stat(f); err != nil {
				return nil, err
			}
			abs = append(abs, f)
		}
	}

	cmd := []string{"docker", "compose"}
	for _, f := range abs {
		cmd = append(cmd, "-f", f)
		if hasContainerName(f) {
			fmt.Fprintf(os.Stderr, "devrun: warning: %s sets container_name; it will collide across worktrees (remove it so compose can namespace containers)\n", f)
		}
	}
	cmd = append(cmd, "up", "-d", "--wait")
	if build {
		cmd = append(cmd, "--build")
	}
	cmd = append(cmd, extra...)
	return &UpPlan{Kind: docker.Kind, Cmd: cmd, PortNames: vars, Primary: vars[0]}, nil
}

// findComposeFile looks for a compose file in dir and its parents, stopping
// at the first directory that contains .git.
func findComposeFile(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		for _, n := range composeNames {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil || filepath.Dir(d) == d {
			break
		}
	}
	return "", fmt.Errorf("no compose file found from %s (looked for %s); pass -f", dir, strings.Join(composeNames, ", "))
}

var containerNameRe = regexp.MustCompile(`^\s*container_name\s*:`)

// hasContainerName is a line-based check (no YAML dependency) for an
// uncommented container_name: key.
func hasContainerName(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if containerNameRe.MatchString(sc.Text()) {
			return true
		}
	}
	return false
}
