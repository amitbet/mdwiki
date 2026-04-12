package ws

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeRedisBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte, bool)
}

func newFakeRedisBus() *fakeRedisBus {
	return &fakeRedisBus{subs: make(map[string][]func([]byte, bool))}
}

type fakeRedisPubSub struct {
	bus *fakeRedisBus
	id  string
}

func (f *fakeRedisPubSub) Publish(room string, data []byte, isBinary bool) {
	f.bus.mu.Lock()
	subs := append([]func([]byte, bool){}, f.bus.subs[room]...)
	f.bus.mu.Unlock()
	for _, sub := range subs {
		sub(data, isBinary)
	}
}

func (f *fakeRedisPubSub) Subscribe(ctx context.Context, room string, fn func([]byte, bool)) error {
	wrapped := func(data []byte, isBinary bool) {
		if f.id == "origin" && string(data) == "skip-origin" {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
			fn(data, isBinary)
		}
	}

	f.bus.mu.Lock()
	f.bus.subs[room] = append(f.bus.subs[room], wrapped)
	f.bus.mu.Unlock()

	<-ctx.Done()
	return ctx.Err()
}

func TestHubRedisBroadcastsAcrossServers(t *testing.T) {
	bus := newFakeRedisBus()
	hubA := NewHub(&fakeRedisPubSub{bus: bus, id: "a"})
	hubB := NewHub(&fakeRedisPubSub{bus: bus, id: "b"})
	go hubA.Run()
	go hubB.Run()

	clientA := &Client{ID: "a", Room: "space:page.md", Send: make(chan []byte, 4), TextSend: make(chan []byte, 4), Hub: hubA, ControlOnly: true}
	clientB := &Client{ID: "b", Room: "space:page.md", Send: make(chan []byte, 4), TextSend: make(chan []byte, 4), Hub: hubB, ControlOnly: true}

	hubA.Register(clientA)
	hubB.Register(clientB)
	t.Cleanup(func() {
		hubA.Unregister(clientA)
		hubB.Unregister(clientB)
	})

	hubA.BroadcastYjs(clientA.Room, []byte("hello"), clientA)

	select {
	case got := <-clientB.Send:
		if string(got) != "hello" {
			t.Fatalf("redis fan-out payload = %q, want hello", string(got))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for redis fan-out")
	}
}

func TestHubRedisBroadcastsControlAcrossServers(t *testing.T) {
	bus := newFakeRedisBus()
	hubA := NewHub(&fakeRedisPubSub{bus: bus, id: "a"})
	hubB := NewHub(&fakeRedisPubSub{bus: bus, id: "b"})
	go hubA.Run()
	go hubB.Run()

	clientA := &Client{ID: "a", Room: "space:__watch__", Send: make(chan []byte, 4), TextSend: make(chan []byte, 4), Hub: hubA, ControlOnly: true}
	clientB := &Client{ID: "b", Room: "space:__watch__", Send: make(chan []byte, 4), TextSend: make(chan []byte, 4), Hub: hubB, ControlOnly: true}

	hubA.Register(clientA)
	hubB.Register(clientB)
	t.Cleanup(func() {
		hubA.Unregister(clientA)
		hubB.Unregister(clientB)
	})

	ctrl := mustMarshalControl(Control{Type: MsgPagesInvalidated})
	hubA.broadcast <- broadcastMsg{room: clientA.Room, data: ctrl, skip: nil, isBinary: false}

	select {
	case got := <-clientB.TextSend:
		if string(got) != string(ctrl) {
			t.Fatalf("redis control payload = %q, want %q", string(got), string(ctrl))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for redis control fan-out")
	}
}
