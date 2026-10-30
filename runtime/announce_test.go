package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
)

type recordingPublisher struct {
	mu       sync.Mutex
	versions []string
}

func (r *recordingPublisher) Publish(_ context.Context, kind uint32, payload []byte) error {
	if kind != configcontract.EventConfigChanged {
		return nil
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.versions = append(r.versions, body.Version)
	return nil
}

func (r *recordingPublisher) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.versions...)
}

func TestAnnounceSendsEachNewVersionOnce(t *testing.T) {
	publisher := &recordingPublisher{}
	pending := make(changes, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go announce(ctx, publisher, pending)

	wait := func(n int) []string {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got := publisher.snapshot(); len(got) >= n {
				return got
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("published %v, want %d", publisher.snapshot(), n)
		return nil
	}
	pending.offer("v1")
	wait(1)
	pending.offer("v1") // the module's own write, seen again by the watch
	pending.offer("v2")
	got := wait(2)
	time.Sleep(20 * time.Millisecond)
	if got = publisher.snapshot(); len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Fatalf("published %v, want v1 then v2 once each", got)
	}
}

func TestPendingKeepsOnlyTheNewestVersion(t *testing.T) {
	pending := make(changes, 1)
	pending.offer("v1")
	pending.offer("v2")
	pending.offer("v3")
	if got := <-pending; got != "v3" {
		t.Fatalf("pending = %q, want the newest, v3", got)
	}
	select {
	case extra := <-pending:
		t.Fatalf("pending held a second version %q", extra)
	default:
	}
}
