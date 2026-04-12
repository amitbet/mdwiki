package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/config"
	"mdwiki/internal/redisx"
	"mdwiki/internal/session"
	"mdwiki/internal/space"
	wshub "mdwiki/internal/ws"
)

func newRedisBackedServer(t *testing.T) (*Server, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	ps, err := redisx.New(redisx.Options{Enabled: true, Addrs: []string{mr.Addr()}})
	if err != nil {
		t.Fatalf("redisx.New: %v", err)
	}
	t.Cleanup(func() {
		if ps != nil && ps.Client != nil {
			_ = ps.Client.Close()
		}
		mr.Close()
	})
	store := &fakeSettingsStore{load: appsettings.Settings{
		RootRepoURL:      "https://github.com/amitbet/documents",
		RootRepoBranch:   "main",
		RootRepoLocalDir: filepath.Join(t.TempDir(), "root"),
		StorageDir:       filepath.Join(t.TempDir(), "state"),
		SaveMode:         "git_sync",
	}}
	srv := &Server{
		Cfg: config.Config{
			RootRepoURL:      store.load.RootRepoURL,
			RootRepoBranch:   "main",
			RootRepoLocalDir: store.load.RootRepoLocalDir,
			StorageDir:       store.load.StorageDir,
			SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
			FrontendOrigin:   "http://localhost:5173",
			ServerGitToken:   "server-token",
		},
		Registry:    &space.Registry{},
		Store:       store,
		Sessions:    session.NewStore(),
		Hub:         wshub.NewHub(nil),
		Redis:       ps,
		deviceFlows: make(map[string]*deviceFlowEntry),
	}
	return srv, mr
}

func TestRedisGitStateRoundTripAndWait(t *testing.T) {
	srv, _ := newRedisBackedServer(t)
	ctx := context.Background()

	state := gitJobState{Status: "queued", Result: gitWriteResult{OK: true, Message: "queued"}}
	if err := srv.storeGitJobState(ctx, "job-1", state); err != nil {
		t.Fatalf("storeGitJobState: %v", err)
	}
	got, err := srv.getGitJobState(ctx, "job-1")
	if err != nil {
		t.Fatalf("getGitJobState: %v", err)
	}
	if got.JobID != "job-1" || got.Status != "queued" || got.Result.Message != "queued" {
		t.Fatalf("unexpected state: %+v", got)
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = srv.storeGitJobState(ctx, "job-wait", gitJobState{Status: "succeeded", Result: gitWriteResult{OK: true, Commit: "abc123", Message: "done"}})
		close(done)
	}()
	res, err := srv.waitForGitJobResult(ctx, "job-wait", 2*time.Second)
	if err != nil {
		t.Fatalf("waitForGitJobResult: %v", err)
	}
	if !res.OK || res.Commit != "abc123" {
		t.Fatalf("unexpected waited result: %+v", res)
	}
	<-done
}

func TestEnqueueGitWriteDedupesAndKeys(t *testing.T) {
	srv, _ := newRedisBackedServer(t)
	ctx := context.Background()
	job := gitWriteJob{ID: "job-queue", RepoRoot: "/tmp/repo", Branch: "main", Path: "docs/page.md"}

	first, err := srv.enqueueGitWrite(ctx, job)
	if err != nil {
		t.Fatalf("enqueueGitWrite first: %v", err)
	}
	second, err := srv.enqueueGitWrite(ctx, job)
	if err != nil {
		t.Fatalf("enqueueGitWrite second: %v", err)
	}
	if first.JobID != "job-queue" || second.JobID != "job-queue" {
		t.Fatalf("unexpected job IDs: %+v %+v", first, second)
	}

	state, err := srv.getGitJobState(ctx, "job-queue")
	if err != nil {
		t.Fatalf("getGitJobState queued: %v", err)
	}
	if state.Status != "queued" || state.Path != "docs/page.md" {
		t.Fatalf("queued state mismatch: %+v", state)
	}
	if got := srv.redisGitJobStateKey("job-queue"); got != "mdwiki:git:{queue}:job:job-queue" {
		t.Fatalf("redisGitJobStateKey = %q", got)
	}
	if got := gitJobPath(gitWriteJob{Path: "a.md", NotifyPath: "b.md"}); got != "b.md" {
		t.Fatalf("gitJobPath = %q", got)
	}
}

func TestRunQueuedGitJobStoresTerminalState(t *testing.T) {
	srv, _ := newRedisBackedServer(t)
	repo := initGitMainRepoWithOrigin(t)

	job := gitWriteJob{
		ID:          "job-run-1",
		Op:          "save",
		RepoRoot:    repo,
		Branch:      "main",
		Path:        "docs/page.md",
		NotifyPath:  "docs/page.md",
		Content:     "hello from queue\n",
		AuthorName:  "Test User",
		AuthorEmail: "test@example.com",
	}
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}

	srv.runQueuedGitJob(context.Background(), redis.XMessage{
		ID: "1-0",
		Values: map[string]any{
			"payload": string(payload),
		},
	})

	state, err := srv.getGitJobState(context.Background(), "job-run-1")
	if err != nil {
		t.Fatalf("getGitJobState after run: %v", err)
	}
	if state.Status != "succeeded" || !state.Result.OK || state.Result.Path != "docs/page.md" {
		t.Fatalf("terminal state mismatch: %+v", state)
	}
}

func TestTerminalStateHelpersAndRenewLock(t *testing.T) {
	srv, _ := newRedisBackedServer(t)
	job := gitWriteJob{ID: "job-1", Path: "docs/page.md"}
	state := terminalStateForResult(job, gitWriteResult{OK: false, Error: "boom"})
	if state.Status != "failed" || state.Path != "docs/page.md" {
		t.Fatalf("terminalStateForResult mismatch: %+v", state)
	}
	if got := shortCommitHash("1234567890abcdef"); got != "1234567890ab" {
		t.Fatalf("shortCommitHash = %q", got)
	}

	ok, err := srv.Redis.TryAcquireLock(context.Background(), "lock-queue", "owner-1", time.Second)
	if err != nil || !ok {
		t.Fatalf("TryAcquireLock: ok=%v err=%v", ok, err)
	}
	stop := make(chan struct{})
	lost := make(chan struct{})
	go srv.renewGitLock("lock-queue", "owner-1", time.Second, stop, lost)
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	select {
	case <-lost:
		t.Fatalf("renewGitLock should keep ownership")
	default:
	}
}
