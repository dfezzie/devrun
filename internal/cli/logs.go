package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/exterkamp/devrun/internal/runner"
)

func init() {
	Register(Command{Name: "logs", Summary: "show the server log: logs [target] [-f] [-n lines]", Run: cmdLogs})
}

func cmdLogs(env *Env, args []string) error {
	fs := newFlagSet(env, "logs")
	follow := fs.Bool("f", false, "follow the log")
	lines := fs.Int("n", 100, "number of trailing lines to show (0 for all)")
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
	e, err := env.resolveEntry(f, "logs", name)
	if err != nil {
		return err
	}
	rn, err := runner.Get(e.Kind)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rn.Logs(ctx, *e, runner.LogOpts{Follow: *follow, Lines: *lines}, env.Stdout)
}
