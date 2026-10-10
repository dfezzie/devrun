package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/exterkamp/devrun/internal/herdr"
	"github.com/exterkamp/devrun/internal/registry"
)

func init() {
	Register(Command{Name: "notify", Summary: `flag a server for human inspection: notify [target] "message" [--json]`, Run: cmdNotify})
	Register(Command{Name: "clear", Summary: "remove the inspection flag: clear [target]", Run: cmdClear})
}

// splitNameMessage interprets `notify [target] "message"`: one positional is the
// message, two are name then message.
func splitNameMessage(pos []string) (name, msg string, err error) {
	switch len(pos) {
	case 1:
		return "", pos[0], nil
	case 2:
		return pos[0], pos[1], nil
	}
	return "", "", fmt.Errorf(`usage: devrun notify [target] "message"`)
}

func cmdNotify(env *Env, args []string) error {
	fs := newFlagSet(env, "notify")
	asJSON := fs.Bool("json", false, "machine-readable output")
	sound := fs.String("sound", "request", "herdr sound: none, done or request")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	name, msg, err := splitNameMessage(pos)
	if err != nil {
		return err
	}
	if strings.TrimSpace(msg) == "" {
		return fmt.Errorf("empty message")
	}

	// Record the flag first: it must stick even if herdr is missing or slow.
	var e registry.Entry
	err = env.Store.Update(func(f *registry.File) error {
		cur, err := env.resolveEntry(f, "notify", name)
		if err != nil {
			return err
		}
		cur.Flagged = &registry.Flag{At: env.Now().UTC(), Message: msg}
		e = *cur
		return nil
	})
	if err != nil {
		return err
	}

	title, body := notifyText(&e, msg)
	delivered, reason := deliver(title, body, *sound)
	if !delivered {
		// Fallback: the flag is recorded; make sure a human can still see it.
		fmt.Fprintf(env.Stderr, "devrun: NEEDS INSPECTION %s | %s | %s\n", title, body, "("+reason+")")
	}
	if *asJSON {
		return json.NewEncoder(env.Stdout).Encode(map[string]interface{}{
			"flagged": true, "key": e.Key, "url": e.URL(), "notified": delivered, "reason": reason,
		})
	}
	return nil
}

// notifyText builds the toast. The URL leads the body so truncation eats the
// message tail rather than the URL.
func notifyText(e *registry.Entry, msg string) (title, body string) {
	title = fmt.Sprintf("%s/%s: inspect :%d", e.Project, filepath.Base(e.Worktree), e.Port())
	body = strings.TrimSpace(e.URL() + " " + msg)
	return title, body
}

// deliver shows the toast via herdr when running in a pane. It never fails the
// caller: the second result says why nothing was shown.
func deliver(title, body, sound string) (bool, string) {
	if !herdr.InPane() {
		return false, "not running inside herdr"
	}
	if _, ok := herdr.Binary(); !ok {
		return false, "herdr not found"
	}
	res, err := herdr.Notify(title, body, sound)
	if err != nil {
		return false, err.Error()
	}
	if !res.Shown {
		return false, "herdr did not show it: " + res.Reason
	}
	return true, res.Reason
}

func cmdClear(env *Env, args []string) error {
	fs := newFlagSet(env, "clear")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	return env.Store.Update(func(f *registry.File) error {
		e, err := env.resolveEntry(f, "clear", name)
		if err != nil {
			return err
		}
		if e.Flagged == nil {
			fmt.Fprintf(env.Stderr, "devrun: %s was not flagged\n", e.Name)
		}
		e.Flagged = nil
		return nil
	})
}
