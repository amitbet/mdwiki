package api

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mdwiki/internal/gitops"
)

func TestRenewErrorForfeitsLock(t *testing.T) {
	now := time.Now()

	if renewErrorForfeitsLock(now, now.Add(4*time.Minute+59*time.Second), 5*time.Minute) {
		t.Fatalf("should keep lock before TTL fully elapses")
	}
	if !renewErrorForfeitsLock(now, now.Add(5*time.Minute), 5*time.Minute) {
		t.Fatalf("should forfeit lock once TTL elapses without a confirmed renewal")
	}
	if renewErrorForfeitsLock(now, now.Add(-time.Second), 5*time.Minute) {
		t.Fatalf("clock skew backwards should not immediately forfeit lock")
	}
	if !renewErrorForfeitsLock(now, now, 0) {
		t.Fatalf("non-positive TTL should fail closed")
	}
}

func TestRunGitWriteJobSaveReplayDoesNotCreateSecondCommit(t *testing.T) {
	repo := initGitMainRepoWithOrigin(t)

	job := gitWriteJob{
		ID:          "job-save-replay",
		Op:          "save",
		RepoRoot:    repo,
		Branch:      "main",
		Path:        "docs/page.md",
		Content:     "hello\n",
		AuthorName:  "Test User",
		AuthorEmail: "test@example.com",
	}

	first := runGitWriteJob(context.Background(), job)
	if !first.OK {
		t.Fatalf("first run failed: %+v", first)
	}
	if got := gitCommitCount(t, repo); got != 2 {
		t.Fatalf("commit count after first run = %d, want 2", got)
	}

	second := runGitWriteJob(context.Background(), job)
	if !second.OK {
		t.Fatalf("replayed run failed: %+v", second)
	}
	if got := gitCommitCount(t, repo); got != 2 {
		t.Fatalf("commit count after replay = %d, want 2", got)
	}
	if second.Commit == "" {
		t.Fatalf("replayed run should report the applied commit")
	}
}

func TestRunGitWriteJobDeleteReplayDoesNotCreateSecondCommit(t *testing.T) {
	repo := initGitMainRepoWithOrigin(t)

	if err := gitops.WriteFileCommitLocalWithJob(context.Background(), repo, "main", "docs/page.md", []byte("hello\n"), "Test User", "test@example.com", "seed file", nil, "seed-job"); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	job := gitWriteJob{
		ID:          "job-delete-replay",
		Op:          "delete",
		RepoRoot:    repo,
		Branch:      "main",
		Path:        "docs/page.md",
		CommitMsg:   "delete page",
		AuthorName:  "Test User",
		AuthorEmail: "test@example.com",
	}

	first := runGitWriteJob(context.Background(), job)
	if !first.OK {
		t.Fatalf("first delete failed: %+v", first)
	}
	if got := gitCommitCount(t, repo); got != 3 {
		t.Fatalf("commit count after first delete = %d, want 3", got)
	}

	second := runGitWriteJob(context.Background(), job)
	if !second.OK {
		t.Fatalf("replayed delete failed: %+v", second)
	}
	if got := gitCommitCount(t, repo); got != 3 {
		t.Fatalf("commit count after replayed delete = %d, want 3", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs", "page.md")); !os.IsNotExist(err) {
		t.Fatalf("page should remain deleted after replay, err=%v", err)
	}
}

func initGitMainRepoWithOrigin(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "commit", "--allow-empty", "-m", "seed")
	runGit(t, repo, "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	return repo
}

func gitCommitCount(t *testing.T, repo string) int {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-list", "--count", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v\n%s", err, out)
	}
	var count int
	if _, err := fmt.Sscanf(string(out), "%d", &count); err != nil {
		t.Fatalf("parse commit count %q: %v", string(out), err)
	}
	return count
}

func runGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
