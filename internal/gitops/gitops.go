package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Service performs git operations on a cloned repo root.
type Service struct {
	Root string
}

// EnsureClone clones repoURL into root if missing.
func EnsureClone(root, repoURL, branch, authUser, token string) (*git.Repository, error) {
	normalizedBranch := normalizeBranchName(branch)
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		r, openErr := git.PlainOpen(root)
		if openErr != nil {
			return nil, openErr
		}
		if err := ensureRemoteOrigin(r, repoURL); err != nil {
			return nil, err
		}
		if err := EnsureBranch(root, normalizedBranch); err != nil {
			return nil, err
		}
		return r, nil
	}
	_ = os.MkdirAll(filepath.Dir(root), 0o755)
	url := injectToken(repoURL, authUser, token)
	opts := &git.CloneOptions{URL: url}
	if normalizedBranch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(normalizedBranch)
		opts.SingleBranch = true
	}
	r, err := git.PlainClone(root, false, opts)
	if err == nil {
		return r, nil
	}

	// Empty remote repos cannot be cloned yet; initialize local repo and wire origin.
	if strings.Contains(strings.ToLower(err.Error()), "remote repository is empty") {
		r, initErr := EnsureRepo(root)
		if initErr != nil {
			return nil, initErr
		}
		if err := ensureRemoteOrigin(r, repoURL); err != nil {
			return nil, err
		}
		if checkoutErr := EnsureBranch(root, normalizedBranch); checkoutErr != nil {
			return nil, checkoutErr
		}
		return r, nil
	}
	return nil, err
}

func ensureRemoteOrigin(r *git.Repository, repoURL string) error {
	rem, err := r.Remote("origin")
	if err == nil {
		cfg := rem.Config()
		if len(cfg.URLs) > 0 && strings.TrimSpace(cfg.URLs[0]) == strings.TrimSpace(repoURL) {
			return nil
		}
		_ = r.DeleteRemote("origin")
	}
	_, err = r.CreateRemote(&gitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "remote already exists") {
		return err
	}
	return nil
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

func injectToken(repoURL, authUser, token string) string {
	if token == "" {
		return repoURL
	}
	user := strings.TrimSpace(authUser)
	if user == "" {
		user = "git"
	}
	if strings.HasPrefix(repoURL, "https://") {
		return strings.Replace(repoURL, "https://", "https://"+user+":"+token+"@", 1)
	}
	return repoURL
}

func normalizeBranchName(branch string) string {
	target := strings.TrimSpace(branch)
	for {
		next := strings.TrimPrefix(strings.TrimPrefix(target, "refs/heads/"), "heads/")
		if next == target {
			break
		}
		target = next
	}
	target = strings.Trim(target, "/")
	if target == "" {
		return "main"
	}
	return target
}

// Pull fast-forwards origin.
func Pull(root, authUser, token string) error {
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
		user := strings.TrimSpace(authUser)
		if user == "" {
			user = "git"
		}
		opts.Auth = &githttp.BasicAuth{Username: user, Password: token}
	}
	return w.Pull(opts)
}

// WriteFileOnly writes content to disk without any git add/commit/push.
func WriteFileOnly(root, relPath, content string) error {
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// Push pushes local commits to origin when configured.
func Push(root, authUser, token, branch string) error {
	r, err := git.PlainOpen(root)
	if err != nil {
		return err
	}
	opts := &git.PushOptions{RemoteName: "origin"}
	targetBranch := normalizeBranchName(branch)
	opts.RefSpecs = []gitcfg.RefSpec{
		gitcfg.RefSpec("+" + "HEAD:refs/heads/" + targetBranch),
	}
	if token != "" {
		user := strings.TrimSpace(authUser)
		if user == "" {
			user = "git"
		}
		opts.Auth = &githttp.BasicAuth{Username: user, Password: token}
	}
	err = r.Push(opts)
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
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
func WritePageLocal(root, branch, relPath, content, authorName, authorEmail string, coAuthors []string) error {
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
	if err := EnsureBranch(root, branch); err != nil {
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

// DeleteFileLocal removes a tracked file and creates a local commit when there is a change.
func DeleteFileLocal(root, branch, relPath, authorName, authorEmail string) error {
	full := filepath.Join(root, relPath)
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	r, err := EnsureRepo(root)
	if err != nil {
		return err
	}
	if err := EnsureBranch(root, branch); err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	_, _ = w.Remove(relPath)
	status, err := w.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		return nil
	}
	msg := fmt.Sprintf("wiki: remove %s", relPath)
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

// RenameFileLocal renames a tracked/untracked file path and creates a local commit when there is a change.
func RenameFileLocal(root, branch, oldRelPath, newRelPath, authorName, authorEmail string) error {
	oldFull := filepath.Join(root, oldRelPath)
	newFull := filepath.Join(root, newRelPath)
	if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldFull, newFull); err != nil {
		return err
	}
	r, err := EnsureRepo(root)
	if err != nil {
		return err
	}
	if err := EnsureBranch(root, branch); err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	if _, err := w.Add(newRelPath); err != nil {
		return err
	}
	_, _ = w.Remove(oldRelPath)
	status, err := w.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		return nil
	}
	msg := fmt.Sprintf("wiki: rename %s -> %s", oldRelPath, newRelPath)
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

// EnsureBranch checks out an existing branch or creates it.
func EnsureBranch(root, branch string) error {
	target := normalizeBranchName(branch)
	r, err := git.PlainOpen(root)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	ref := plumbing.NewBranchReferenceName(target)
	if head, headErr := r.Head(); headErr == nil && head.Name().IsBranch() && head.Name() == ref {
		return nil
	}
	err = w.Checkout(&git.CheckoutOptions{Branch: ref})
	if err == nil {
		return nil
	}
	lowerErr := strings.ToLower(err.Error())
	isMissing := errors.Is(err, plumbing.ErrReferenceNotFound) ||
		strings.Contains(lowerErr, "reference not found") ||
		strings.Contains(lowerErr, "branch not found")
	if !isMissing {
		return err
	}
	// Branch missing locally, create it from current HEAD.
	return w.Checkout(&git.CheckoutOptions{Branch: ref, Create: true})
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
