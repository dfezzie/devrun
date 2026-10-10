package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

func init() {
	Register(Command{Name: "watch", Summary: "live view of servers, for a herdr pane: watch [--project P] [--interval 1s] [--once]", Run: cmdWatch})
}

// Terminal control sequences used by watch.
const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	cursorHome   = "\x1b[H"
	clearToEOL   = "\x1b[K"
	clearBelow   = "\x1b[J"
)

func cmdWatch(env *Env, args []string) error {
	fs := newFlagSet(env, "watch")
	project := fs.String("project", "", "only show this project")
	interval := fs.Duration("interval", time.Second, "refresh interval")
	once := fs.Bool("once", false, "print one frame and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval < 100*time.Millisecond {
		*interval = 100 * time.Millisecond
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tty := isTTY(env.Stdout)
	return watchLoop(ctx, env, *project, *interval, *once || !tty, tty)
}

// views loads the current server list, sorted like ls, without dead entries.
func (env *Env) views(project string) ([]View, error) {
	f, err := env.Store.Load()
	if err != nil {
		return nil, err
	}
	listening, err := env.Listening()
	if err != nil {
		return nil, err
	}
	vs := []View{}
	for _, e := range f.Entries {
		if project != "" && e.Project != project {
			continue
		}
		v := env.view(e, listening)
		if v.State == StateDead {
			continue
		}
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Worktree != b.Worktree {
			return a.Worktree < b.Worktree
		}
		return a.Name < b.Name
	})
	return vs, nil
}

// watchLoop redraws until ctx ends. With single set it prints one plain frame
// (no screen control) and returns. Newly flagged entries, those that were not
// flagged when the view opened or on the previous refresh, are marked NEW
// until they are cleared.
func watchLoop(ctx context.Context, env *Env, project string, interval time.Duration, single, tty bool) error {
	color := tty && os.Getenv("NO_COLOR") == ""
	seen := map[string]time.Time{} // key -> flag time already acknowledged at open
	newKeys := map[string]bool{}
	first := true
	var lastFrame string

	frame := func() (string, error) {
		vs, err := env.views(project)
		if err != nil {
			return "", err
		}
		live := map[string]bool{}
		for _, v := range vs {
			if v.Flagged == nil {
				delete(newKeys, v.Key)
				delete(seen, v.Key)
				continue
			}
			live[v.Key] = true
			if prev, ok := seen[v.Key]; !ok && !first || ok && !prev.Equal(v.Flagged.At) {
				newKeys[v.Key] = true
			}
			seen[v.Key] = v.Flagged.At
		}
		for k := range newKeys {
			if !live[k] {
				delete(newKeys, k)
			}
		}
		first = false
		w := termWidth(env.Stdout)
		if c := os.Getenv("COLUMNS"); !tty && c != "" {
			fmt.Sscan(c, &w)
		}
		lines := RenderFrame(vs, RenderOpts{Width: w, Color: color, Now: env.Now(), NewKeys: newKeys, Project: project})
		return strings.Join(lines, "\n"), nil
	}

	if single {
		f, err := frame()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(env.Stdout, f)
		return err
	}

	out := env.Stdout
	io.WriteString(out, altScreenOn+hideCursor)
	defer io.WriteString(out, showCursor+altScreenOff)

	draw := func() error {
		f, err := frame()
		if err != nil {
			f = "devrun: " + err.Error()
		}
		if f == lastFrame {
			return nil
		}
		lastFrame = f
		// One write per frame: home, overwrite each line in place (clearing
		// only its tail), then clear what is left below. No full-screen clear,
		// so nothing flashes.
		var b bytes.Buffer
		b.WriteString(cursorHome)
		for _, l := range strings.Split(f, "\n") {
			b.WriteString(l + clearToEOL + "\n")
		}
		b.WriteString(clearBelow)
		_, werr := out.Write(b.Bytes())
		return werr
	}
	if err := draw(); err != nil {
		return err
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := draw(); err != nil {
				return err
			}
		}
	}
}
