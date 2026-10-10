// Package cli implements the devrun command line. Each command lives in its own
// file and registers itself from init, so new commands never edit this file.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/exterkamp/devrun/internal/alloc"
	"github.com/exterkamp/devrun/internal/registry"
)

// Env is everything a command touches, so tests can substitute pieces.
type Env struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Cwd            string
	Store          *registry.Store
	Alloc          *alloc.Allocator
	Now            func() time.Time
	Listening      func() (map[int]bool, error)
}

// Command is one subcommand.
type Command struct {
	Name    string
	Summary string
	Run     func(env *Env, args []string) error
}

var commands = map[string]Command{}

// Register adds a command. Call from init.
func Register(c Command) { commands[c.Name] = c }

// exitError carries a specific exit code (message already printed or empty).
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// NewEnv builds the production environment.
func NewEnv() (*Env, error) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &Env{
		Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin,
		Cwd: cwd, Store: &registry.Store{Dir: dir}, Alloc: alloc.New(),
		Now: time.Now, Listening: alloc.Listening,
	}, nil
}

// Main runs devrun with args (excluding argv[0]) and returns the exit code.
func Main(args []string) int {
	env, err := NewEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "devrun:", err)
		return 1
	}
	return Run(env, args)
}

// Run dispatches a subcommand against env.
func Run(env *Env, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(env.Stderr)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(env.Stderr, "devrun: unknown command %q\n\n", args[0])
		usage(env.Stderr)
		return 2
	}
	if err := cmd.Run(env, args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		if ee, ok := err.(exitError); ok {
			if ee.msg != "" {
				fmt.Fprintln(env.Stderr, "devrun:", ee.msg)
			}
			return ee.code
		}
		fmt.Fprintln(env.Stderr, "devrun:", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: devrun <command> [flags]")
	fmt.Fprintln(w)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-8s %s\n", n, commands[n].Summary)
	}
}

// newFlagSet returns a FlagSet that reports errors via return values.
func newFlagSet(env *Env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet("devrun "+name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}

// parseInterspersed parses flags that may appear before or after positional
// arguments (`devrun logs web -f`) and returns the positionals.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
