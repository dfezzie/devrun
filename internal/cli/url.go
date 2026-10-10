package cli

import (
	"encoding/json"
	"fmt"
)

func init() {
	Register(Command{Name: "url", Summary: "print a server URL: url [target] [--json]", Run: cmdURL})
}

func cmdURL(env *Env, args []string) error {
	fs := newFlagSet(env, "url")
	asJSON := fs.Bool("json", false, "machine-readable output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	f, err := env.Store.Load()
	if err != nil {
		return err
	}
	e, err := env.resolveEntry(f, "url", name)
	if err != nil {
		return err
	}
	listening, err := env.Listening()
	if err != nil {
		return err
	}
	v := env.view(e, listening)
	if v.State == StateDead {
		return fmt.Errorf("server %q is not running", e.Name)
	}
	if *asJSON {
		return json.NewEncoder(env.Stdout).Encode(map[string]interface{}{
			"name": v.Name, "url": v.URL, "port": v.Port, "ports": v.Ports, "state": v.State,
		})
	}
	fmt.Fprintln(env.Stdout, v.URL)
	return nil
}
