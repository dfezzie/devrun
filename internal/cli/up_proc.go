package cli

import (
	"errors"
	"flag"

	"github.com/exterkamp/devrun/internal/proc"
)

func init() {
	RegisterUpMode(UpMode{Name: "proc", Bind: func(fs *flag.FlagSet) func([]string) (*UpPlan, error) {
		portEnv := fs.String("port-env", "PORT", "environment variable that receives the port")
		return func(rest []string) (*UpPlan, error) {
			if len(rest) == 0 {
				return nil, errors.New("nothing to run: usage: devrun up [--name web] -- <cmd...>")
			}
			return &UpPlan{Kind: proc.Kind, Cmd: rest, PortNames: []string{*portEnv}, Primary: *portEnv}, nil
		}
	}})
}
