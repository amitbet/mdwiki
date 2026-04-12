package redisx

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestPubSub(t *testing.T) (*miniredis.Miniredis, *PubSub) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	ps, err := New(Options{Enabled: true, Addrs: []string{mr.Addr()}})
	if err != nil {
		t.Fatalf("redisx.New: %v", err)
	}
	t.Cleanup(func() {
		if ps != nil && ps.Client != nil {
			_ = ps.Client.Close()
		}
		mr.Close()
	})
	return mr, ps
}

func TestHelpers(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := compact([]string{" a ", "", "b", "   "}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("compact = %#v", got)
	}
	streams := []redis.XStream{{Messages: []redis.XMessage{{ID: "1"}}}, {Messages: []redis.XMessage{{ID: "2"}}}}
	flat := flattenMessages(streams)
	if len(flat) != 2 || flat[0].ID != "1" || flat[1].ID != "2" {
		t.Fatalf("flattenMessages = %#v", flat)
	}
	enc := EncodeBytesBase64([]byte("hello"))
	dec, err := DecodeBytesBase64(enc)
	if err != nil || string(dec) != "hello" {
		t.Fatalf("base64 roundtrip = %q err=%v", string(dec), err)
	}
}

func TestSetGetLockAndQueueHelpers(t *testing.T) {
	_, ps := newTestPubSub(t)
	ctx := context.Background()

	if err := ps.Set(ctx, "key-1", []byte("value-1"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, err := ps.Get(ctx, "key-1")
	if err != nil || string(val) != "value-1" {
		t.Fatalf("Get = %q err=%v", string(val), err)
	}

	ok, err := ps.TryAcquireLock(ctx, "lock-1", "owner-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("TryAcquireLock ok=%v err=%v", ok, err)
	}
	ok, err = ps.RenewLock(ctx, "lock-1", "owner-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("RenewLock ok=%v err=%v", ok, err)
	}
	if err := ps.ReleaseLock(ctx, "lock-1", "owner-1"); err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}
	ok, err = ps.TryAcquireLock(ctx, "lock-1", "owner-2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("TryAcquireLock after release ok=%v err=%v", ok, err)
	}

	if err := ps.Enqueue(ctx, "queue-1", []byte("job-1")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, err := ps.DequeueBlocking(ctx, "queue-1", time.Second)
	if err != nil || string(job) != "job-1" {
		t.Fatalf("DequeueBlocking = %q err=%v", string(job), err)
	}

	if err := ps.PublishResult(ctx, "result-1", []byte("done"), time.Minute); err != nil {
		t.Fatalf("PublishResult: %v", err)
	}
	result, err := ps.WaitResult(ctx, "result-1", time.Second)
	if err != nil || string(result) != "done" {
		t.Fatalf("WaitResult = %q err=%v", string(result), err)
	}
}

func TestPublishSubscribeAndStreamQueue(t *testing.T) {
	mr, sub := newTestPubSub(t)
	pub, err := New(Options{Enabled: true, Addrs: []string{mr.Addr()}})
	if err != nil {
		t.Fatalf("second redisx.New: %v", err)
	}
	defer pub.Client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgCh := make(chan struct {
		data []byte
		bin  bool
	}, 1)
	go func() {
		_ = sub.Subscribe(ctx, "room-1", func(data []byte, isBinary bool) {
			msgCh <- struct {
				data []byte
				bin  bool
			}{data: data, bin: isBinary}
		})
	}()
	time.Sleep(100 * time.Millisecond)

	pub.Publish("room-1", []byte("hello"), true)
	select {
	case got := <-msgCh:
		if string(got.data) != "hello" || !got.bin {
			t.Fatalf("subscribe payload mismatch: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for pubsub message")
	}

	if err := sub.EnsureConsumerGroup(context.Background(), "stream-1", "group-1"); err != nil {
		t.Fatalf("EnsureConsumerGroup: %v", err)
	}
	enqueued, err := sub.EnqueueStreamOnce(context.Background(), "stream-1", "job-1", []byte(`{"state":"queued"}`), time.Minute, []byte(`{"payload":1}`))
	if err != nil || !enqueued {
		t.Fatalf("EnqueueStreamOnce first ok=%v err=%v", enqueued, err)
	}
	enqueued, err = sub.EnqueueStreamOnce(context.Background(), "stream-1", "job-1", []byte(`{"state":"queued"}`), time.Minute, []byte(`{"payload":1}`))
	if err != nil || enqueued {
		t.Fatalf("EnqueueStreamOnce dedupe ok=%v err=%v", enqueued, err)
	}
	msgs, err := sub.ReadGroup(context.Background(), "stream-1", "group-1", "consumer-1", time.Second, 10)
	if err != nil {
		t.Fatalf("ReadGroup: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("ReadGroup got %d messages", len(msgs))
	}
	if err := sub.AckStream(context.Background(), "stream-1", "group-1", msgs[0].ID); err != nil {
		t.Fatalf("AckStream: %v", err)
	}
}
