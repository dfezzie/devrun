package alloc

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/exterkamp/devrun/internal/registry"
)

func none() (map[int]bool, error) { return nil, nil }

func newAlloc(src ...Source) *Allocator {
	if src == nil {
		src = []Source{none}
	}
	return &Allocator{Min: MinPort, Max: MaxPort, Sources: src}
}

func TestParseProcNetTCP(t *testing.T) {
	in := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:A029 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0
   1: 0100007F:1F90 0100007F:D2E4 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0
   2: 00000000000000000000000001000000:A02A 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 3 1 0
`
	got := map[int]bool{}
	if err := ParseProcNetTCP(strings.NewReader(in), got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0xA029] || !got[0xA02A] || got[0x1F90] {
		t.Fatalf("got %v", got)
	}
}

func TestListeningSeesRealListener(t *testing.T) {
	ln, port := listen(t)
	defer ln.Close()
	m, err := Listening()
	if err != nil || !m[port] {
		t.Fatalf("listener %d not seen: %v %v", port, m[port], err)
	}
}

func TestStableAndInRange(t *testing.T) {
	a := newAlloc()
	f := &registry.File{}
	p1, err := a.Allocate(f, "proj:/wt:web", []string{"PORT"})
	if err != nil {
		t.Fatal(err)
	}
	if p := p1["PORT"]; p < MinPort || p > MaxPort {
		t.Fatalf("out of range: %d", p)
	}
	// Same file again, and a fresh file: same answer.
	p2, _ := a.Allocate(f, "proj:/wt:web", []string{"PORT"})
	p3, _ := a.Allocate(&registry.File{}, "proj:/wt:web", []string{"PORT"})
	if p1["PORT"] != p2["PORT"] || p1["PORT"] != p3["PORT"] {
		t.Fatalf("unstable: %v %v %v", p1, p2, p3)
	}
}

func TestSkipsRegistryReservedAndListening(t *testing.T) {
	a := &Allocator{Min: 100, Max: 104, Sources: []Source{func() (map[int]bool, error) {
		return map[int]bool{101: true}, nil
	}}}
	f := &registry.File{Reserved: map[string]int{"other#PORT": 102}}
	f.Upsert(&registry.Entry{Key: "other", Ports: map[string]int{"PORT": 100}})
	got := map[int]bool{}
	for _, k := range []string{"a", "b"} {
		m, err := a.Allocate(f, k, []string{"PORT"})
		if err != nil {
			t.Fatal(err)
		}
		got[m["PORT"]] = true
		f.Upsert(&registry.Entry{Key: k, Ports: m})
	}
	// 100 registry, 101 listening, 102 reserved: only 103/104 remain.
	if len(got) != 2 || !got[103] || !got[104] {
		t.Fatalf("got %v", got)
	}
	// Range now full except the reserved port: falls back to it rather than failing.
	m, err := a.Allocate(f, "c", []string{"PORT"})
	if err != nil || m["PORT"] != 102 {
		t.Fatalf("fallback: %v %v", m, err)
	}
	f.Upsert(&registry.Entry{Key: "c", Ports: m})
	if _, err := a.Allocate(f, "d", []string{"PORT"}); err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func TestReservedPortReusedUnlessBusy(t *testing.T) {
	busy := map[int]bool{}
	a := newAlloc(func() (map[int]bool, error) { return busy, nil })
	f := &registry.File{}
	m, _ := a.Allocate(f, "k", []string{"PORT"})
	orig := m["PORT"]
	busy[orig] = true // something else grabbed it while stopped
	m2, err := a.Allocate(f, "k", []string{"PORT"})
	if err != nil || m2["PORT"] == orig {
		t.Fatalf("should move off busy port: %v %v", m2, err)
	}
}

func TestMultiplePortsDistinct(t *testing.T) {
	a := &Allocator{Min: 100, Max: 103, Sources: []Source{none}}
	m, err := a.Allocate(&registry.File{}, "k", []string{"WEB_PORT", "API_PORT", "DB_PORT"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, p := range m {
		seen[p] = true
	}
	if len(seen) != 3 {
		t.Fatalf("duplicates: %v", m)
	}
}

func TestSourceError(t *testing.T) {
	a := newAlloc(func() (map[int]bool, error) { return nil, fmt.Errorf("boom") })
	if _, err := a.Allocate(&registry.File{}, "k", []string{"PORT"}); err == nil {
		t.Fatal("expected error")
	}
}

// allocInto allocates ports for keys and records them in the store, as `up`
// does, returning port->key pairs.
func allocInto(s *registry.Store, a *Allocator, keys []string) (map[string]int, error) {
	out := map[string]int{}
	for _, k := range keys {
		err := s.Update(func(f *registry.File) error {
			m, err := a.Allocate(f, k, []string{"PORT"})
			if err != nil {
				return err
			}
			f.Upsert(&registry.Entry{Key: k, Ports: m})
			out[k] = m["PORT"]
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func TestConcurrentGoroutinesNeverShare(t *testing.T) {
	s := &registry.Store{Dir: t.TempDir()}
	a := newAlloc()
	const n = 300 // enough that hash collisions are certain in 1000 slots
	var wg sync.WaitGroup
	var mu sync.Mutex
	byPort := map[int]string{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("proj:/wt/%d:web", i)
			got, err := allocInto(s, a, []string{k})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if other, dup := byPort[got[k]]; dup {
				t.Errorf("port %d given to both %s and %s", got[k], other, k)
			}
			byPort[got[k]] = k
		}(i)
	}
	wg.Wait()
	f, _ := s.Load()
	if len(f.Entries) != n {
		t.Fatalf("entries=%d", len(f.Entries))
	}
}

// Child process mode for TestConcurrentProcessesNeverShare.
func TestMain(m *testing.M) {
	if dir := os.Getenv("ALLOC_TEST_CHILD_DIR"); dir != "" {
		id := os.Getenv("ALLOC_TEST_CHILD_ID")
		var keys []string
		for i := 0; i < 25; i++ {
			keys = append(keys, fmt.Sprintf("proj:/wt/%s-%d:web", id, i))
		}
		if _, err := allocInto(&registry.Store{Dir: dir}, newAlloc(), keys); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestConcurrentProcessesNeverShare(t *testing.T) {
	dir := t.TempDir()
	const procs = 12
	var wg sync.WaitGroup
	for i := 0; i < procs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=NONE")
			cmd.Env = append(os.Environ(), "ALLOC_TEST_CHILD_DIR="+dir, fmt.Sprintf("ALLOC_TEST_CHILD_ID=%d", i))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child %d: %v\n%s", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	f, _ := (&registry.Store{Dir: dir}).Load()
	if len(f.Entries) != procs*25 {
		t.Fatalf("entries=%d want %d", len(f.Entries), procs*25)
	}
	seen := map[int]string{}
	for _, e := range f.Entries {
		p := e.Port()
		if other, dup := seen[p]; dup {
			t.Fatalf("port %d shared by %s and %s", p, other, e.Key)
		}
		seen[p] = e.Key
	}
}
