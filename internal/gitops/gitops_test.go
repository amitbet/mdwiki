package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeBranchNameAndBase64Helpers(t *testing.T) {
	tests := map[string]string{
		"main": "main",
		"refs/heads/main": "main",
		"heads/feature/test": "feature/test",
		"": "main",
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
