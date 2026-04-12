package redisx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRedisIntegration(t *testing.T) {
	if os.Getenv("MDWIKI_REDIS_INTEGRATION") != "1" {
		t.Skip("set MDWIKI_REDIS_INTEGRATION=1 to run redis integration tests")
	}

	opts := Options{
		Enabled:     true,
		URL:         os.Getenv("MDWIKI_REDIS_URL"),
		Addrs:       splitCSV(os.Getenv("MDWIKI_REDIS_ADDRS")),
		ClusterMode: os.Getenv("MDWIKI_REDIS_CLUSTER_MODE") == "1" || os.Getenv("MDWIKI_REDIS_CLUSTER_MODE") == "true",
		Username:    os.Getenv("MDWIKI_REDIS_USERNAME"),
		Password:    os.Getenv("MDWIKI_REDIS_PASSWORD"),
	}

	pubA, err := New(opts)
	if err != nil {
		t.Fatalf("redisx.New publisher: %v", err)
	}
	defer pubA.Client.Close()

	pubB, err := New(opts)
	if err != nil {
		t.Fatalf("redisx.New subscriber: %v", err)
	}
	defer pubB.Client.Close()

	t.Run("pubsub", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		room := "integration:" + uuid.NewString()
		received := make(chan []byte, 1)
		errCh := make(chan error, 1)

		go func() {
			errCh <- pubB.Subscribe(ctx, room, func(data []byte, isBinary bool) {
				if !isBinary {
					t.Errorf("expected binary redis payload")
					return
				}
				select {
				case received <- append([]byte(nil), data...):
				default:
				}
			})
		}()

		time.Sleep(200 * time.Millisecond)
		pubA.Publish(room, []byte("hello-cluster"), true)

		select {
		case got := <-received:
			if string(got) != "hello-cluster" {
				t.Fatalf("pubsub payload = %q", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for pubsub payload")
		}

		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("subscribe returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for subscriber shutdown")
		}
	})

	t.Run("streams_and_lock", func(t *testing.T) {
		ctx := context.Background()
		streamTag := "{itest-" + uuid.NewString() + "}"
		streamKey := "mdwiki:test:" + streamTag + ":stream"
		jobKey := "mdwiki:test:" + streamTag + ":job"
		resultKey := "mdwiki:test:result:" + uuid.NewString()
		lockKey := "mdwiki:test:lock:" + uuid.NewString()

		enqueued, err := pubA.EnqueueStreamOnce(ctx, streamKey, jobKey, []byte(`{"status":"queued"}`), time.Minute, []byte(`{"id":"job-1"}`))
		if err != nil {
			t.Fatalf("enqueue stream once: %v", err)
		}
		if !enqueued {
			t.Fatalf("expected first enqueue to succeed")
		}
		enqueued, err = pubA.EnqueueStreamOnce(ctx, streamKey, jobKey, []byte(`{"status":"queued"}`), time.Minute, []byte(`{"id":"job-1"}`))
		if err != nil {
			t.Fatalf("enqueue duplicate: %v", err)
		}
		if enqueued {
			t.Fatalf("expected duplicate enqueue to be suppressed")
		}

		if err := pubB.EnsureConsumerGroup(ctx, streamKey, "workers"); err != nil {
			t.Fatalf("ensure consumer group: %v", err)
		}
		msgs, err := pubB.ReadGroup(ctx, streamKey, "workers", "worker-a", 5*time.Second, 4)
		if err != nil {
			t.Fatalf("read group: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 stream message, got %d", len(msgs))
		}
		payloadStr, ok := msgs[0].Values["payload"].(string)
		if !ok {
			t.Fatalf("stream payload type = %T", msgs[0].Values["payload"])
		}
		if payloadStr != `{"id":"job-1"}` {
			t.Fatalf("stream payload = %#v", payloadStr)
		}
		if err := pubB.AckStream(ctx, streamKey, "workers", msgs[0].ID); err != nil {
			t.Fatalf("ack stream: %v", err)
		}

		if err := pubA.PublishResult(ctx, resultKey, []byte("done"), time.Minute); err != nil {
			t.Fatalf("publish result: %v", err)
		}
		got, err := pubB.WaitResult(ctx, resultKey, 5*time.Second)
		if err != nil {
			t.Fatalf("wait result: %v", err)
		}
		if string(got) != "done" {
			t.Fatalf("result payload = %q", got)
		}

		ok, err = pubA.TryAcquireLock(ctx, lockKey, "owner-a", time.Minute)
		if err != nil {
			t.Fatalf("acquire lock: %v", err)
		}
		if !ok {
			t.Fatalf("expected first lock acquisition to succeed")
		}

		ok, err = pubB.TryAcquireLock(ctx, lockKey, "owner-b", time.Minute)
		if err != nil {
			t.Fatalf("acquire competing lock: %v", err)
		}
		if ok {
			t.Fatalf("expected competing lock acquisition to fail")
		}
		ok, err = pubA.RenewLock(ctx, lockKey, "owner-a", time.Minute)
		if err != nil {
			t.Fatalf("renew lock: %v", err)
		}
		if !ok {
			t.Fatalf("expected lock renewal to succeed")
		}

		if err := pubA.ReleaseLock(ctx, lockKey, "owner-a"); err != nil {
			t.Fatalf("release lock: %v", err)
		}
	})
}

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
