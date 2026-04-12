package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeBranchNameAndBase64Helpers(t *testing.T) {
	tests := map[string]string{
		"main":               "main",
		"refs/heads/main":    "main",
		"heads/feature/test": "feature/test",
		"":                   "main",
	}
	for in, want := range tests {
		if got := normalizeBranchName(in); got != want {
			t.Fatalf("normalizeBranchName(%q) = %q, want %q", in, got, want)
		}
	}
	encoded := EncodeBytesBase64([]byte("hello world"))
	decoded, err := DecodeBytesBase64(encoded)
	if err != nil {
		t.Fatalf("DecodeBytesBase64 unexpected error: %v", err)
	}
	if string(decoded) != "hello world" {
		t.Fatalf("decoded = %q", string(decoded))
	}
}

func TestLastCommitForPathAndReadFileAtCommit(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
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
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	path := filepath.Join(repo, "docs")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	filePath := filepath.Join(path, "page.md")
	if err := os.WriteFile(filePath, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "first")
	commit1 := run("rev-parse", "HEAD")

	if err := os.WriteFile(filePath, []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "second")
	commit2 := run("rev-parse", "HEAD")

	gotLast, err := LastCommitForPath(repo, filepath.ToSlash(filepath.Join("docs", "page.md")))
	if err != nil {
		t.Fatalf("LastCommitForPath unexpected error: %v", err)
	}
	if gotLast != commit2 {
		t.Fatalf("LastCommitForPath = %q, want %q", gotLast, commit2)
	}

	oldContent, err := ReadFileAtCommit(repo, commit1, filepath.ToSlash(filepath.Join("docs", "page.md")))
	if err != nil {
		t.Fatalf("ReadFileAtCommit unexpected error: %v", err)
	}
	if string(oldContent) != "first\n" {
		t.Fatalf("old content = %q", string(oldContent))
	}
}

func TestWriteFileCommitLocalWithJobAddsDurableMarker(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	if err := WriteFileCommitLocalWithJob(context.Background(), repo, "main", "docs/page.md", []byte("hello\n"), "Test User", "test@example.com", "wiki: update docs/page.md", nil, "job-123"); err != nil {
		t.Fatalf("WriteFileCommitLocalWithJob: %v", err)
	}

	commit, err := FindCommitByJobID(repo, "main", "job-123")
	if err != nil {
		t.Fatalf("FindCommitByJobID: %v", err)
	}
	if commit == "" {
		t.Fatalf("expected durable job marker commit")
	}
}

func TestRenameFileLocalWithJobIsReplaySafe(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	oldPath := filepath.Join(repo, "docs", "old.md")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	if err := WriteFileCommitLocalWithJob(context.Background(), repo, "main", "docs/old.md", []byte("hello\n"), "Test User", "test@example.com", "wiki: add docs/old.md", nil, "seed-job"); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	if err := RenameFileLocalWithJob(context.Background(), repo, "main", "docs/old.md", "docs/new.md", "Test User", "test@example.com", "rename-job"); err != nil {
		t.Fatalf("first RenameFileLocalWithJob: %v", err)
	}
	if err := RenameFileLocalWithJob(context.Background(), repo, "main", "docs/old.md", "docs/new.md", "Test User", "test@example.com", "rename-job"); err != nil {
		t.Fatalf("replayed RenameFileLocalWithJob: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, "docs", "new.md")); err != nil {
		t.Fatalf("new path missing after replay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs", "old.md")); !os.IsNotExist(err) {
		t.Fatalf("old path should be gone after replay, err=%v", err)
	}
}

func TestPushContextRebasesAndRetriesAfterRemoteAdvances(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "origin.git")
	workspace := t.TempDir()
	peer := t.TempDir()

	run := func(repo string, args ...string) string {
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
		return strings.TrimSpace(string(out))
	}

	run(workspace, "init", "-b", "main")
	run(workspace, "init", "--bare", remote)
	run(workspace, "remote", "add", "origin", remote)

	if err := os.WriteFile(filepath.Join(workspace, "seed.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	run(workspace, "add", ".")
	run(workspace, "commit", "-m", "seed")
	run(workspace, "push", "-u", "origin", "main")

	run(peer, "clone", remote, ".")
	run(peer, "checkout", "main")
	if err := os.WriteFile(filepath.Join(peer, "remote.md"), []byte("remote\n"), 0o644); err != nil {
		t.Fatalf("write remote: %v", err)
	}
	run(peer, "add", ".")
	run(peer, "commit", "-m", "remote change")
	run(peer, "push", "origin", "main")

	if err := WriteFileCommitLocalWithJob(context.Background(), workspace, "main", "local.md", []byte("local\n"), "Test User", "test@example.com", "local change", nil, "job-push-retry"); err != nil {
		t.Fatalf("WriteFileCommitLocalWithJob: %v", err)
	}

	if err := PushContext(context.Background(), workspace, "", "", "main"); err != nil {
		t.Fatalf("PushContext: %v", err)
	}

	logOutput := run(workspace, "log", "--oneline", "--format=%s", "-3")
	if !strings.Contains(logOutput, "local change") || !strings.Contains(logOutput, "remote change") {
		t.Fatalf("expected rebased history to contain both commits, got:\n%s", logOutput)
	}

	run(peer, "fetch", "origin", "main")
	remoteLog := run(peer, "log", "origin/main", "--oneline", "--format=%s", "-3")
	if !strings.Contains(remoteLog, "local change") || !strings.Contains(remoteLog, "remote change") {
		t.Fatalf("expected pushed remote history to contain both commits, got:\n%s", remoteLog)
	}
}
