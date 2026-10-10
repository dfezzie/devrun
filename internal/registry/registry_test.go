package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRoundTripAndAtomicity(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	f, err := s.Load()
	if err != nil || len(f.Entries) != 0 {
		t.Fatalf("empty load: %v %v", f, err)
	}
	err = s.Update(func(f *File) error {
		f.Upsert(&Entry{Key: "a", Ports: map[string]int{"PORT": 41001}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f, _ = s.Load()
	if e := f.Find("a"); e == nil || e.Port() != 41001 || e.URL() != "http://127.0.0.1:41001" {
		t.Fatalf("bad entry: %+v", e)
	}
	// no tmp files left behind
	m, _ := filepath.Glob(filepath.Join(s.Dir, "*.tmp"))
	if len(m) != 0 {
		t.Fatalf("leftover tmp files: %v", m)
	}
}

func TestUpdateErrorDiscardsChanges(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	boom := errors.New("boom")
	err := s.Update(func(f *File) error {
		f.Upsert(&Entry{Key: "x"})
		return boom
	})
	if err != boom {
		t.Fatalf("got %v", err)
	}
	f, _ := s.Load()
	if len(f.Entries) != 0 {
		t.Fatal("changes persisted despite error")
	}
}

func TestCorruptRegistry(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	os.WriteFile(s.Path(), []byte("{nope"), 0o644)
	if _, err := s.Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Update(func(f *File) error {
				f.Upsert(&Entry{Key: fmt.Sprint("k", i)})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, _ := s.Load()
	if len(f.Entries) != n {
		t.Fatalf("got %d entries, want %d", len(f.Entries), n)
	}
}

func TestFileHelpers(t *testing.T) {
	f := &File{}
	f.Upsert(&Entry{Key: "a", Ports: map[string]int{"WEB_PORT": 2, "API_PORT": 1}})
	f.Upsert(&Entry{Key: "a", Ports: map[string]int{"PORT": 3}})
	f.Upsert(&Entry{Key: "b", Ports: map[string]int{"PORT": 4}})
	if len(f.Entries) != 2 || f.Find("a").Port() != 3 {
		t.Fatal("upsert")
	}
	if u := f.PortsInUse("a"); len(u) != 1 || !u[4] {
		t.Fatalf("PortsInUse: %v", u)
	}
	if !f.Remove("a") || f.Remove("a") || len(f.Entries) != 1 {
		t.Fatal("remove")
	}
	e := Entry{Ports: map[string]int{"WEB_PORT": 2, "API_PORT": 1}}
	if e.Port() != 1 {
		t.Fatal("alphabetical fallback")
	}
	e.PrimaryPort = "WEB_PORT"
	if e.Port() != 2 {
		t.Fatal("primary")
	}
}
