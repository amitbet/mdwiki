package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Service performs git operations on a cloned repo root.
type Service struct {
	Root string
}

// EnsureClone clones repoURL into root if missing.
func EnsureClone(root, repoURL, branch, token string) (*git.Repository, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return git.PlainOpen(root)
	}
	_ = os.MkdirAll(filepath.Dir(root), 0o755)
	url := injectToken(repoURL, token)
	opts := &git.CloneOptions{URL: url}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		opts.SingleBranch = true
	}
	return git.PlainClone(root, false, opts)
}

// EnsureRepo opens an existing git repository, or initializes a new one.
func EnsureRepo(root string) (*git.Repository, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return git.PlainOpen(root)
	}
	return git.PlainInit(root, false)
}

func injectToken(repoURL, token string) string {
	if token == "" {
		return repoURL
	}
	if strings.HasPrefix(repoURL, "https://") {
		return strings.Replace(repoURL, "https://", "https://oauth2:"+token+"@", 1)
	}
	return repoURL
}

// Pull fast-forwards origin.
func Pull(root, token string) error {
	r, err := git.PlainOpen(root)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	opts := &git.PullOptions{RemoteName: "origin"}
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: "oauth2", Password: token}
	}
	return w.Pull(opts)
}

// WritePage writes relative path content and commits + push.
func WritePage(root, relPath, content, authorName, authorEmail, pusherToken string, coAuthors []string) error {
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	r, err := git.PlainOpen(root)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	if _, err := w.Add(relPath); err != nil {
		return err
	}
	if err := w.AddGlob("."); err != nil {
		return err
	}
	msg := fmt.Sprintf("wiki: update %s", relPath)
	if len(coAuthors) > 0 {
		msg += "\n\n"
		for _, ca := range coAuthors {
			msg += "Co-authored-by: " + ca + "\n"
		}
	}
	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}
	pushOpts := &git.PushOptions{RemoteName: "origin"}
	if pusherToken != "" {
		pushOpts.Auth = &githttp.BasicAuth{Username: "oauth2", Password: pusherToken}
	}
	return r.Push(pushOpts)
}

// WritePageLocal writes relative path content and creates a local commit.
func WritePageLocal(root, relPath, content, authorName, authorEmail string, coAuthors []string) error {
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	r, err := EnsureRepo(root)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	if _, err := w.Add(relPath); err != nil {
		return err
	}
	status, err := w.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		return nil
	}
	msg := fmt.Sprintf("wiki: update %s", relPath)
	if len(coAuthors) > 0 {
		msg += "\n\n"
		for _, ca := range coAuthors {
			msg += "Co-authored-by: " + ca + "\n"
		}
	}
	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if errors.Is(err, git.ErrEmptyCommit) {
		return nil
	}
	return err
}

// EnsureSpaceMeta writes minimal .mdwiki/space.json if missing.
func EnsureSpaceMeta(root, spaceID string) error {
	dir := filepath.Join(root, ".mdwiki")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "space.json")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	body := fmt.Sprintf(`{"schema_version":1,"space_id":%q,"display_name":%q,"default_branch":"main"}
`, spaceID, spaceID)
	return os.WriteFile(p, []byte(body), 0o644)
}

// ReadFile reads a path under repo root.
func ReadFile(root, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, relPath))
}
