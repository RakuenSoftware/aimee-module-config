package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type versions struct {
	mu   sync.Mutex
	seen []string
}

func (v *versions) add(version string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seen = append(v.seen, version)
}

func (v *versions) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v.mu.Lock()
		for _, got := range v.seen {
			if got == want {
				v.mu.Unlock()
				return
			}
		}
		v.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("version %s was never announced", want)
}

func TestACommittedWriteAnnouncesTheVersionReadersSee(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	seen := &versions{}
	store.OnChange(seen.add)
	if err := store.Set("autonomous", true); err != nil {
		t.Fatal(err)
	}
	current, err := store.Version("")
	if err != nil {
		t.Fatal(err)
	}
	if _, snapshotVersion, err := store.Snapshot(); err != nil || snapshotVersion != current {
		t.Fatalf("snapshot version %q, %v; want %q", snapshotVersion, err, current)
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.seen) != 1 || seen.seen[0] != current {
		t.Fatalf("announced %v, want exactly the version now on disk %s", seen.seen, current)
	}
}

func TestARefusedWriteAnnouncesNothing(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	seen := &versions{}
	store.OnChange(seen.add)
	if err := store.Set("no_such_key_anywhere", 1); err == nil {
		t.Fatal("an unknown key must be refused")
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.seen) != 0 {
		t.Fatalf("announced %v for a write that did not commit", seen.seen)
	}
}

func TestWatchAnnouncesAnEditMadeOutsideTheModule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := &versions{}
	store.OnChange(seen.add)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- store.Watch(ctx) }()
	time.Sleep(50 * time.Millisecond) // let the watch be installed

	// An editor's write: a temporary file renamed over the original.
	tmp := path + ".edit"
	if err := os.WriteFile(tmp, []byte("autonomous: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	want, err := store.Version("")
	if err != nil {
		t.Fatal(err)
	}
	seen.waitFor(t, want)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop when its context ended")
	}
}
