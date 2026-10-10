// Package herdr talks to the herdr CLI. Every call goes through the binary
// (never the socket directly), has a timeout, and understands herdr's JSON
// envelope: {"id":...,"result":{...}} or {"id":...,"error":{"code","message"}}.
//
// See docs/herdr-notes.md for the behaviour this encodes.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds every herdr call so a hung server never blocks devrun.
const DefaultTimeout = 3 * time.Second

// APIError is an error envelope returned by herdr.
type APIError struct{ Code, Message string }

func (e *APIError) Error() string { return "herdr: " + e.Code + ": " + e.Message }

// ServerNotRunning reports whether err is herdr's "no server" error.
func ServerNotRunning(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == "server_not_running"
}

// InPane reports whether this process runs inside a herdr pane and has a pane
// id to attribute work to. HERDR_ENV alone is not enough: with no pane id,
// `--current` commands silently resolve to the *focused* pane.
func InPane() bool {
	return os.Getenv("HERDR_ENV") == "1" && os.Getenv("HERDR_PANE_ID") != ""
}

// Binary finds the herdr executable: $HERDR_BIN_PATH, else PATH.
func Binary() (string, bool) {
	if p := os.Getenv("HERDR_BIN_PATH"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	p, err := exec.LookPath("herdr")
	if err != nil {
		return "", false
	}
	return p, true
}

// Run executes herdr with args and returns the envelope's result. A non-zero
// exit with a parseable error envelope yields *APIError.
func Run(timeout time.Duration, args ...string) (json.RawMessage, error) {
	bin, ok := Binary()
	if !ok {
		return nil, errors.New("herdr not found (set HERDR_BIN_PATH or put it on PATH)")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("herdr %s: timed out after %s", args[0], timeout)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &env); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("herdr %s: %v: %s", args[0], runErr, strings.TrimSpace(errb.String()))
		}
		return nil, fmt.Errorf("herdr %s: unparseable output: %v", args[0], err)
	}
	if env.Error != nil {
		return nil, &APIError{Code: env.Error.Code, Message: env.Error.Message}
	}
	if runErr != nil {
		return nil, fmt.Errorf("herdr %s: %v", args[0], runErr)
	}
	return env.Result, nil
}

// Owner is who is running devrun, as far as herdr can tell.
type Owner struct {
	Pane, Workspace, Tab, Agent string
}

// DetectOwner reads the pane/tab/workspace ids from the environment and asks
// herdr for the agent name (falling back to the agent kind). It returns nil
// outside a herdr pane. Lookup failures leave Agent empty but keep the ids.
func DetectOwner() *Owner {
	if !InPane() {
		return nil
	}
	o := &Owner{
		Pane:      os.Getenv("HERDR_PANE_ID"),
		Workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		Tab:       os.Getenv("HERDR_TAB_ID"),
	}
	raw, err := Run(2*time.Second, "agent", "list")
	if err != nil {
		return o
	}
	o.Agent = agentFor(raw, o.Pane)
	return o
}

func agentFor(raw json.RawMessage, pane string) string {
	var r struct {
		Agents []struct {
			Agent  string `json:"agent"`
			Name   string `json:"name"`
			PaneID string `json:"pane_id"`
		} `json:"agents"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	for _, a := range r.Agents {
		if a.PaneID == pane {
			if a.Name != "" {
				return a.Name
			}
			return a.Agent
		}
	}
	return ""
}

// PaneExists reports whether the pane is still in the session.
func PaneExists(pane string) (bool, error) {
	_, err := Run(2*time.Second, "pane", "get", pane)
	var ae *APIError
	if errors.As(err, &ae) && ae.Code != "server_not_running" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Truncate shortens s to at most n runes, ending in an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// NotifyResult is herdr's answer to notification.show.
type NotifyResult struct {
	Shown  bool   `json:"shown"`
	Reason string `json:"reason"`
}

// Notify shows a toast. Title is limited to 80 runes and body to 240 by herdr;
// we truncate first so the URL-first body keeps the part that matters.
func Notify(title, body, sound string) (NotifyResult, error) {
	args := []string{"notification", "show", Truncate(title, 80)}
	if body != "" {
		args = append(args, "--body", Truncate(body, 240))
	}
	if sound != "" {
		args = append(args, "--sound", sound)
	}
	var res NotifyResult
	raw, err := Run(DefaultTimeout, args...)
	if err != nil {
		return res, err
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return res, err
	}
	return res, nil
}

// Snapshot is the subset of `herdr api snapshot` devrun reads.
type Snapshot struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// GetSnapshot fetches `herdr api snapshot`.
func GetSnapshot() (Snapshot, error) {
	var s struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	raw, err := Run(DefaultTimeout, "api", "snapshot")
	if err != nil {
		return Snapshot{}, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot, nil
}

// Version runs `herdr --version`.
func Version() (string, error) {
	bin, ok := Binary()
	if !ok {
		return "", errors.New("herdr not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	return strings.TrimSpace(string(out)), err
}
