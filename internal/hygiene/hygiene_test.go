package hygiene

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProc builds a /proc-shaped tree: net/tcp with LISTEN sockets plus
// per-pid stat and fd symlinks.
type fakeProc struct {
	t    *testing.T
	root string
	tcp  string
}

func newFakeProc(t *testing.T) *fakeProc {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "net"), 0o755)
	return &fakeProc{t: t, root: root, tcp: "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnmt   uid  timeout inode\n"}
}

func (p *fakeProc) listen(port int, inode uint64) {
	p.tcp += fmt.Sprintf("   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 %d 1 0 100 0 0 10 0\n", port, inode)
}

func (p *fakeProc) proc(pid, pgrp int, state string, start uint64, inodes ...uint64) {
	dir := filepath.Join(p.root, fmt.Sprint(pid))
	os.MkdirAll(filepath.Join(dir, "fd"), 0o755)
	// pid (comm with ) paren) state ppid pgrp ... starttime is field 22
	f := make([]string, 22) // fields 3..24 after the comm
	f[0], f[1], f[2] = state, "1", fmt.Sprint(pgrp)
	for i := 3; i < len(f); i++ {
		f[i] = "0"
	}
	f[19] = fmt.Sprint(start)
	stat := fmt.Sprintf("%d (we ird) %s", pid, strings.Join(f, " "))
	os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644)
	for i, ino := range inodes {
		os.Symlink(fmt.Sprintf("socket:[%d]", ino), filepath.Join(dir, "fd", fmt.Sprint(i+3)))
	}
}

func (p *fakeProc) scanner() Scanner {
	os.WriteFile(filepath.Join(p.root, "net", "tcp"), []byte(p.tcp), 0o644)
	return Scanner{Root: p.root}
}

func TestListenersAndGroupPorts(t *testing.T) {
	p := newFakeProc(t)
	p.listen(41237, 100) // group 500, expected port
	p.listen(41555, 101) // group 500, drifted port
	p.listen(41300, 102) // someone else, no registry entry
	p.listen(8080, 103)  // outside the range
	p.proc(500, 500, "S", 7, 100)
	p.proc(501, 500, "S", 8, 101) // child in the same group
	p.proc(600, 600, "S", 9, 102)
	p.proc(700, 700, "S", 10, 103)
	p.proc(800, 800, "Z", 11, 102) // zombie holding a stale fd: ignored
	s := p.scanner()

	ports, err := s.GroupPorts(500)
	if err != nil || len(ports) != 2 || ports[0] != 41237 || ports[1] != 41555 {
		t.Fatalf("group ports %v %v", ports, err)
	}
	if ports, _ := s.GroupPorts(999); len(ports) != 0 {
		t.Fatalf("unknown group has ports %v", ports)
	}

	ls, err := s.Listeners()
	if err != nil {
		t.Fatal(err)
	}
	orph := PortOrphans(ls, map[int]bool{41237: true, 41555: true}, 41000, 41999)
	if len(orph) != 1 || orph[0].Port != 41300 || orph[0].PID != 600 || orph[0].Listen.Start != 9 {
		t.Fatalf("orphans %+v", orph)
	}
}

func TestPortOrphansRangeEdges(t *testing.T) {
	ls := []Listener{{Port: 40999}, {Port: 41000}, {Port: 41999}, {Port: 42000}}
	o := PortOrphans(ls, nil, 41000, 41999)
	if len(o) != 2 || o[0].Port != 41000 || o[1].Port != 41999 {
		t.Fatalf("%+v", o)
	}
}

func TestRealListenerIsFoundInOwnGroup(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	ls, err := Scanner{}.Listeners()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ls {
		if x.Port == port && x.PID == os.Getpid() {
			return
		}
	}
	t.Fatalf("own listener on %d not found in %d listeners", port, len(ls))
}

func startSleep(t *testing.T) (*exec.Cmd, uint64) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	_, _, start, ok := Scanner{}.stat(cmd.Process.Pid)
	if !ok {
		t.Fatal("no stat")
	}
	return cmd, start
}

func TestKillRefusesReusedPID(t *testing.T) {
	cmd, start := startSleep(t)
	// Same PID, but the recorded start time belongs to some earlier process.
	killed, err := Scanner{}.Kill(Listener{PID: cmd.Process.Pid, Start: start + 1}, time.Second)
	if err != nil || killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	if _, _, _, ok := (Scanner{}).stat(cmd.Process.Pid); !ok {
		t.Fatal("process was killed despite start-time mismatch")
	}
}

func TestKillMatchingStart(t *testing.T) {
	cmd, start := startSleep(t)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }() // reap so it is not a zombie
	killed, err := Scanner{}.Kill(Listener{PID: cmd.Process.Pid, Start: start}, 2*time.Second)
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("process still running")
	}
}

func TestKillNeverSelfOrInit(t *testing.T) {
	for _, pid := range []int{os.Getpid(), 1, 0} {
		if k, err := (Scanner{}).Kill(Listener{PID: pid}, time.Millisecond); k || err != nil {
			t.Fatalf("pid %d: %v %v", pid, k, err)
		}
	}
}

type fakeCompose struct{ downed []string }

func (f *fakeCompose) Find(known map[string]bool) ([]string, error) {
	if known["devrun-a"] {
		return nil, nil
	}
	return []string{"devrun-a"}, nil
}
func (f *fakeCompose) Down(p string) error { f.downed = append(f.downed, p); return nil }

func TestComposeDefaultIsNoop(t *testing.T) {
	ps, err := Compose().Find(nil)
	if err != nil || len(ps) != 0 || Compose().Down("devrun-x") != nil {
		t.Fatal("default must be a no-op")
	}
	f := &fakeCompose{}
	SetCompose(f)
	defer SetCompose(nil)
	if ps, _ := Compose().Find(map[string]bool{}); len(ps) != 1 {
		t.Fatal("custom detector not used")
	}
}
