package docker

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Container is the subset of `docker compose ps --format json` we use.
type Container struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// ParseComposePS accepts both output shapes of `compose ps --format json`: a
// single JSON array (compose >= 2.21-ish and 2.17+) or one object per line.
func ParseComposePS(data []byte) ([]Container, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	if data[0] == '[' {
		var cs []Container
		err := json.Unmarshal(data, &cs)
		return cs, err
	}
	var cs []Container
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var c Container
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// Running reports whether any container is in a live state.
func Running(cs []Container) bool {
	for _, c := range cs {
		switch strings.ToLower(c.State) {
		case "running", "restarting", "created", "paused":
			return true
		}
	}
	return false
}

// ParsePublished extracts host ports from `docker ps --format {{.Ports}}`
// lines such as "0.0.0.0:8083->80/tcp, :::8083->80/tcp, 0.0.0.0:9000-9002->9000-9002/tcp".
func ParsePublished(out string) map[int]bool {
	ports := map[int]bool{}
	for _, item := range strings.FieldsFunc(out, func(r rune) bool { return r == ',' || r == '\n' }) {
		item = strings.TrimSpace(item)
		i := strings.Index(item, "->")
		if i < 0 {
			continue // unpublished, e.g. "80/tcp"
		}
		host := item[:i]
		if j := strings.LastIndexByte(host, ':'); j >= 0 {
			host = host[j+1:]
		}
		lo, hi := host, host
		if k := strings.IndexByte(host, '-'); k >= 0 {
			lo, hi = host[:k], host[k+1:]
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || b < a || b-a > 65535 {
			continue
		}
		for p := a; p <= b; p++ {
			ports[p] = true
		}
	}
	return ports
}
