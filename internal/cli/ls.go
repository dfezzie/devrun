package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/exterkamp/devrun/internal/registry"
)

func init() {
	Register(Command{Name: "ls", Summary: "list servers: ls [--project P] [--json] [--all]", Run: cmdLs})
}

// View is the stable --json shape of a server.
type View struct {
	Key        string          `json:"key"`
	Project    string          `json:"project"`
	Worktree   string          `json:"worktree"`
	Branch     string          `json:"branch"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	State      string          `json:"state"`
	PID        int             `json:"pid"`
	Port       int             `json:"port"`
	Ports      map[string]int  `json:"ports"`
	URL        string          `json:"url"`
	StartedAt  time.Time       `json:"started_at"`
	AgeSeconds int64           `json:"age_seconds"`
	Log        string          `json:"log"`
	Owner      *registry.Owner `json:"owner"`
	Flagged    *registry.Flag  `json:"flagged"`
	Warnings   []string        `json:"warnings,omitempty"`
}

func (env *Env) view(e *registry.Entry, listening map[int]bool) View {
	v := View{
		Key: e.Key, Project: e.Project, Worktree: e.Worktree, Branch: e.Branch, Name: e.Name,
		Kind: e.Kind, State: stateOf(e, listening), PID: e.PID, Port: e.Port(), Ports: e.Ports,
		URL: e.URL(), StartedAt: e.StartedAt, AgeSeconds: int64(env.Now().Sub(e.StartedAt).Seconds()),
		Log: e.Log, Owner: e.Owner, Flagged: e.Flagged,
	}
	if extra := driftPorts(e, listening[e.Port()]); len(extra) > 0 {
		v.Warnings = append(v.Warnings, driftMessage(e, extra))
	}
	return v
}

func cmdLs(env *Env, args []string) error {
	fs := newFlagSet(env, "ls")
	project := fs.String("project", "", "only show this project")
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "include dead servers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	env.stopStale()
	f, err := env.Store.Load()
	if err != nil {
		return err
	}
	listening, err := env.Listening()
	if err != nil {
		return err
	}
	views := []View{}
	for _, e := range f.Entries {
		if *project != "" && e.Project != *project {
			continue
		}
		v := env.view(e, listening)
		if v.State == StateDead && !*all {
			continue
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool {
		a, b := views[i], views[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Worktree != b.Worktree {
			return a.Worktree < b.Worktree
		}
		return a.Name < b.Name
	})

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(views)
	}
	if len(views) == 0 {
		fmt.Fprintln(env.Stderr, "no servers running")
		return nil
	}
	var tbl bytes.Buffer
	tw := tabwriter.NewWriter(&tbl, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tWORKTREE\tBRANCH\tNAME\tPORT\tURL\tPID\tKIND\tSTATE\tAGE\tOWNER")
	for _, v := range views {
		owner := ""
		if v.Owner != nil {
			owner = v.Owner.HerdrPane
		}
		flag := ""
		if v.Flagged != nil {
			flag = " !"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s\t%s\t%s\n",
			v.Project, filepath.Base(v.Worktree), v.Branch, v.Name, v.Port, v.URL, v.PID,
			v.Kind, v.State+flag, humanAge(time.Duration(v.AgeSeconds)*time.Second), owner)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// Highlight flagged rows after alignment so escapes don't skew column widths.
	color := useColor(env.Stdout)
	rows := strings.Split(strings.TrimRight(tbl.String(), "\n"), "\n")
	for i, row := range rows {
		if i > 0 && views[i-1].Flagged != nil {
			row = paint(color, ansiBold+ansiYellow, row)
		}
		fmt.Fprintln(env.Stdout, row)
	}
	for _, v := range views {
		for _, w := range v.Warnings {
			fmt.Fprintf(env.Stderr, "devrun: warning: %s/%s: %s\n", v.Project, filepath.Base(v.Worktree), w)
		}
	}
	return nil
}
