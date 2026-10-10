package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/exterkamp/devrun/internal/herdr"
)

// ANSI helpers. Color is only emitted when the caller says the output is a
// terminal; the layout code itself never depends on escape sequences for width.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRev    = "\x1b[7m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
)

func paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return code + s + ansiReset
}

// isTTY reports whether w is a character device (a terminal).
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// useColor honours NO_COLOR.
func useColor(w io.Writer) bool {
	return isTTY(w) && os.Getenv("NO_COLOR") == ""
}

// termWidth returns the terminal width of w, or a fallback.
func termWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		var ws struct{ Row, Col, X, Y uint16 }
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e == 0 && ws.Col > 0 {
			return int(ws.Col)
		}
	}
	return 80
}

// stateColor picks the color of a state word.
func stateColor(state string) string {
	switch state {
	case StateUp:
		return ansiGreen
	case StateDead:
		return ansiRed
	}
	return ansiYellow
}

func ownerLabel(v View) string {
	if v.Owner == nil {
		return "-"
	}
	switch {
	case v.Owner.Agent != "" && v.Owner.HerdrPane != "":
		return v.Owner.Agent + "@" + v.Owner.HerdrPane
	case v.Owner.HerdrPane != "":
		return v.Owner.HerdrPane
	case v.Owner.Agent != "":
		return v.Owner.Agent
	}
	return "-"
}

// RenderOpts controls RenderFrame.
type RenderOpts struct {
	Width int
	Color bool
	Now   time.Time
	// NewKeys are flagged entries to mark NEW (flagged since the view opened).
	NewKeys map[string]bool
	Project string
}

// RenderFrame lays out the server list for the given width. Every returned
// line is at most Width visible cells; it contains no trailing newline.
// Wide terminals get a table; narrow ones get two lines per server.
func RenderFrame(views []View, o RenderOpts) []string {
	w := o.Width
	if w < 20 {
		w = 20
	}
	title := "devrun"
	if o.Project != "" {
		title += " · " + o.Project
	}
	flagged := 0
	for _, v := range views {
		if v.Flagged != nil {
			flagged++
		}
	}
	head := fmt.Sprintf("%s  %d server(s)", title, len(views))
	if flagged > 0 {
		head += fmt.Sprintf("  %d flagged", flagged)
	}
	lines := []string{paint(o.Color, ansiBold, herdr.Truncate(head, w))}
	if len(views) == 0 {
		return append(lines, paint(o.Color, ansiDim, herdr.Truncate("no servers running", w)))
	}
	if w >= 100 {
		return append(lines, wideRows(views, o, w)...)
	}
	return append(lines, narrowRows(views, o, w)...)
}

func flagTag(v View, o RenderOpts) string {
	if v.Flagged == nil {
		return ""
	}
	if o.NewKeys[v.Key] {
		return "NEW !"
	}
	return "!"
}

func wideRows(views []View, o RenderOpts, w int) []string {
	cols := []int{14, 18, 8, 6, 12, 5, 22}
	hdr := []string{"PROJECT", "WORKTREE", "NAME", "PORT", "STATE", "AGE", "OWNER"}
	fix := 0
	for _, c := range cols {
		fix += c + 1
	}
	var out []string
	var h strings.Builder
	for i, c := range cols {
		h.WriteString(pad(hdr[i], c) + " ")
	}
	h.WriteString("URL")
	out = append(out, paint(o.Color, ansiDim, herdr.Truncate(h.String(), w)))
	for _, v := range views {
		state := v.State
		if t := flagTag(v, o); t != "" {
			state += " " + t
		}
		age := humanAge(o.Now.Sub(v.StartedAt))
		cells := []string{v.Project, filepath.Base(v.Worktree), v.Name, fmt.Sprint(v.Port), state, age, ownerLabel(v)}
		var b strings.Builder
		for i, c := range cols {
			b.WriteString(pad(cells[i], c) + " ")
		}
		b.WriteString(v.URL)
		out = append(out, rowStyle(v, o, herdr.Truncate(b.String(), w)))
		if v.Flagged != nil {
			msg := "  ↳ " + v.Flagged.Message
			out = append(out, paint(o.Color, ansiYellow, herdr.Truncate(msg, w)))
		}
	}
	return out
}

func narrowRows(views []View, o RenderOpts, w int) []string {
	var out []string
	for _, v := range views {
		l1 := fmt.Sprintf("%s %s :%d %s", dot(v), v.Name, v.Port, v.State)
		if t := flagTag(v, o); t != "" {
			l1 += " " + t
		}
		out = append(out, rowStyle(v, o, herdr.Truncate(l1, w)))
		l2 := fmt.Sprintf("  %s/%s %s", v.Project, filepath.Base(v.Worktree), v.URL)
		out = append(out, paint(o.Color, ansiDim, herdr.Truncate(l2, w)))
		if v.Flagged != nil {
			out = append(out, paint(o.Color, ansiYellow, herdr.Truncate("  ↳ "+v.Flagged.Message, w)))
		}
	}
	return out
}

func dot(v View) string {
	if v.State == StateUp {
		return "●"
	}
	return "○"
}

// rowStyle: flagged rows are bold yellow, NEW ones reverse video; dead rows red.
func rowStyle(v View, o RenderOpts, s string) string {
	switch {
	case v.Flagged != nil && o.NewKeys[v.Key]:
		return paint(o.Color, ansiRev+ansiBold, s)
	case v.Flagged != nil:
		return paint(o.Color, ansiBold+ansiYellow, s)
	case v.State == StateDead:
		return paint(o.Color, ansiRed, s)
	}
	return s
}

// pad truncates or right-pads s to exactly n runes.
func pad(s string, n int) string {
	s = herdr.Truncate(s, n)
	if k := n - len([]rune(s)); k > 0 {
		s += strings.Repeat(" ", k)
	}
	return s
}
