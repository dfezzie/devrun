package alloc

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Listening returns every TCP port with a listener on this host, parsed from
// /proc/net/tcp{,6}. If /proc is unreadable it falls back to `ss -ltn`.
func Listening() (map[int]bool, error) {
	ports := map[int]bool{}
	found := false
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		found = true
		err = ParseProcNetTCP(f, ports)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if found {
		return ports, nil
	}
	return listeningViaSS()
}

// ParseProcNetTCP adds the local port of every LISTEN (state 0A) socket in a
// /proc/net/tcp-formatted stream to ports.
func ParseProcNetTCP(r io.Reader, ports map[int]bool) error {
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		i := strings.LastIndexByte(fields[1], ':')
		if i < 0 {
			continue
		}
		p, err := strconv.ParseUint(fields[1][i+1:], 16, 16)
		if err != nil {
			continue
		}
		ports[int(p)] = true
	}
	return sc.Err()
}

func listeningViaSS() (map[int]bool, error) {
	out, err := exec.Command("ss", "-ltnH").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot determine listening ports (no /proc/net/tcp, ss failed): %w", err)
	}
	ports := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		local := fields[3]
		if i := strings.LastIndexByte(local, ':'); i >= 0 {
			if p, err := strconv.Atoi(local[i+1:]); err == nil {
				ports[p] = true
			}
		}
	}
	return ports, nil
}
