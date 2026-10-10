package hygiene

// Orphan is something devrun did not register but that sits in devrun's
// territory: a listener in the port range, or a devrun-* compose project.
type Orphan struct {
	Kind    string   `json:"kind"` // "port" or "compose"
	Port    int      `json:"port,omitempty"`
	PID     int      `json:"pid,omitempty"`
	Project string   `json:"compose_project,omitempty"`
	Listen  Listener `json:"-"`
}

// PortOrphans returns listeners with a port in [min,max] that is not in
// registered.
func PortOrphans(ls []Listener, registered map[int]bool, min, max int) []Orphan {
	var out []Orphan
	for _, l := range ls {
		if l.Port < min || l.Port > max || registered[l.Port] {
			continue
		}
		out = append(out, Orphan{Kind: "port", Port: l.Port, PID: l.PID, Listen: l})
	}
	return out
}

// ComposeDetector finds and removes compose projects devrun would own (name
// starting with "devrun-") that are missing from the registry. The default is
// a no-op; the docker milestone installs a real one with SetCompose.
type ComposeDetector interface {
	// Find returns orphan compose project names. known holds the compose
	// projects present in the registry.
	Find(known map[string]bool) ([]string, error)
	// Down brings one orphan project down. Volumes are kept.
	Down(project string) error
}

type nopCompose struct{}

func (nopCompose) Find(map[string]bool) ([]string, error) { return nil, nil }
func (nopCompose) Down(string) error                      { return nil }

var compose ComposeDetector = nopCompose{}

// SetCompose installs the compose orphan detector (call from an init in the
// docker kind's package). nil restores the no-op.
func SetCompose(d ComposeDetector) {
	if d == nil {
		d = nopCompose{}
	}
	compose = d
}

// Compose returns the current compose orphan detector.
func Compose() ComposeDetector { return compose }
