package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const jobIDTrailerPrefix = "MDWiki-Job-ID: "

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
	return WriteFileBytesOnly(root, relPath, []byte(content))
}

// WriteFileBytesOnly writes raw bytes to disk without any git add/commit/push.
func WriteFileBytesOnly(root, relPath string, content []byte) error {
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o644)
}

// Push pushes local commits to origin when configured.
func Push(root, authUser, token, branch string) error {
	return PushContext(context.Background(), root, authUser, token, branch)
}

// PushContext pushes local commits to origin when configured and honors cancellation.
func PushContext(ctx context.Context, root, authUser, token, branch string) error {
	targetBranch := normalizeBranchName(branch)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := gitPushCLI(ctx, root, authUser, token, targetBranch); err != nil {
		return err
	}
	return nil
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
	return WriteFileCommitLocal(root, branch, relPath, []byte(content), authorName, authorEmail, fmt.Sprintf("wiki: update %s", relPath), coAuthors)
}

// WriteFileCommitLocal writes raw bytes and creates a local commit with the provided message.
func WriteFileCommitLocal(root, branch, relPath string, content []byte, authorName, authorEmail, commitMessage string, coAuthors []string) error {
	return WriteFileCommitLocalWithJob(context.Background(), root, branch, relPath, content, authorName, authorEmail, commitMessage, coAuthors, "")
}

// WriteFileCommitLocalWithJob writes raw bytes and creates a local commit with an optional durable job marker.
func WriteFileCommitLocalWithJob(ctx context.Context, root, branch, relPath string, content []byte, authorName, authorEmail, commitMessage string, coAuthors []string, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := strings.TrimSpace(commitMessage)
	if msg == "" {
		msg = fmt.Sprintf("wiki: update %s", relPath)
	}
	if len(coAuthors) > 0 {
		msg += "\n\n"
		for _, ca := range coAuthors {
			msg += "Co-authored-by: " + ca + "\n"
		}
	}
	msg = appendJobIDTrailer(msg, jobID)
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
	return DeleteFileLocalWithMessage(root, branch, relPath, authorName, authorEmail, "")
}

// DeleteFileLocalWithMessage removes a tracked file and creates a local commit when there is a change.
func DeleteFileLocalWithMessage(root, branch, relPath, authorName, authorEmail, commitMessage string) error {
	return DeleteFileLocalWithJob(context.Background(), root, branch, relPath, authorName, authorEmail, commitMessage, "")
}

// DeleteFileLocalWithJob removes a tracked file and creates a local commit with an optional durable job marker.
func DeleteFileLocalWithJob(ctx context.Context, root, branch, relPath, authorName, authorEmail, commitMessage, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := strings.TrimSpace(commitMessage)
	if msg == "" {
		msg = fmt.Sprintf("wiki: remove %s", relPath)
	}
	msg = appendJobIDTrailer(msg, jobID)
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
	return RenameFileLocalWithJob(context.Background(), root, branch, oldRelPath, newRelPath, authorName, authorEmail, "")
}

// RenameFileLocalWithJob renames a file path and creates a local commit with an optional durable job marker.
func RenameFileLocalWithJob(ctx context.Context, root, branch, oldRelPath, newRelPath, authorName, authorEmail, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oldFull := filepath.Join(root, oldRelPath)
	newFull := filepath.Join(root, newRelPath)
	if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldFull, newFull); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, newErr := os.Stat(newFull); newErr == nil {
				goto stageRename
			}
		}
		return err
	}
stageRename:
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
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := fmt.Sprintf("wiki: rename %s -> %s", oldRelPath, newRelPath)
	msg = appendJobIDTrailer(msg, jobID)
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
	if head, headErr := r.Head(); headErr == nil {
		if head.Name().IsBranch() && head.Name() == ref {
			return nil
		}
	} else {
		lowerHeadErr := strings.ToLower(headErr.Error())
		isMissingHead := errors.Is(headErr, plumbing.ErrReferenceNotFound) ||
			strings.Contains(lowerHeadErr, "reference not found") ||
			strings.Contains(lowerHeadErr, "not found")
		if isMissingHead {
			return r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, ref))
		}
		return headErr
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

// ReadFileAtCommit reads a repository-relative file as of the provided commit.
func ReadFileAtCommit(root, commit, relPath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show", fmt.Sprintf("%s:%s", commit, relPath))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

// HeadCommit returns the full HEAD commit for the repository.
func HeadCommit(root string) (string, error) {
	return gitRevParse(root, "HEAD")
}

// LastCommitForPath returns the last commit touching the repository-relative path, or HEAD if none exist yet.
func LastCommitForPath(root, relPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "log", "-n", "1", "--format=%H", "--", relPath)
	out, err := cmd.Output()
	if err == nil {
		if got := strings.TrimSpace(string(out)); got != "" {
			return got, nil
		}
	}
	return HeadCommit(root)
}

// FindCommitByJobID returns the newest commit on the branch with the durable job trailer.
func FindCommitByJobID(root, branch, jobID string) (string, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", nil
	}
	targetBranch := normalizeBranchName(branch)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "log", "refs/heads/"+targetBranch, "--fixed-strings", "--grep", jobIDTrailerPrefix+jobID, "--format=%H", "-n", "1")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// EncodeBytesBase64 returns standard base64 for transport/storage convenience.
func EncodeBytesBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeBytesBase64 decodes standard base64 text.
func DecodeBytesBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}

func gitPushCLI(parent context.Context, root, authUser, token, branch string) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	if err := gitRunAuth(ctx, root, authUser, token, "push", "origin", "HEAD:refs/heads/"+branch); err == nil {
		return nil
	} else if !isNonFastForwardPushError(err.Error()) {
		return err
	}

	if err := gitFetchOriginBranchAuth(parent, root, authUser, token, branch); err != nil {
		return err
	}
	if err := gitRebaseOntoOriginBranch(parent, root, branch); err != nil {
		return err
	}
	retryCtx, retryCancel := context.WithTimeout(parent, 30*time.Second)
	defer retryCancel()
	return gitRunAuth(retryCtx, root, authUser, token, "push", "origin", "HEAD:refs/heads/"+branch)
}

func gitRevParse(root, rev string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", rev)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitLsRemoteHead(root, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-remote", "origin", "refs/heads/"+branch)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("remote branch refs/heads/%s not found", branch)
	}
	return strings.TrimSpace(fields[0]), nil
}

func gitFetchOriginBranch(root, branch string) error {
	return gitFetchOriginBranchAuth(context.Background(), root, "", "", branch)
}

func gitFetchOriginBranchAuth(parent context.Context, root, authUser, token, branch string) error {
	targetBranch := normalizeBranchName(branch)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	return gitRunAuth(ctx, root, authUser, token, "fetch", "origin", "refs/heads/"+targetBranch+":refs/remotes/origin/"+targetBranch)
}

func gitRebaseOntoOriginBranch(parent context.Context, root, branch string) error {
	targetBranch := normalizeBranchName(branch)
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := gitRunAuth(ctx, root, "", "", "rebase", "refs/remotes/origin/"+targetBranch); err != nil {
		abortCtx, abortCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer abortCancel()
		_ = gitRunAuth(abortCtx, root, "", "", "rebase", "--abort")
		return fmt.Errorf("remote branch changed and automatic rebase failed: %w", err)
	}
	return nil
}

func gitRunAuth(ctx context.Context, root, authUser, token string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")

	cleanup := func() {}
	if strings.TrimSpace(token) != "" {
		user := strings.TrimSpace(authUser)
		if user == "" {
			user = "git"
		}
		scriptPath, err := writeAskpassScript(user, token)
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.Remove(scriptPath) }
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+scriptPath)
	}
	defer cleanup()

	var stderr bytes.Buffer
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func isNonFastForwardPushError(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(lower, "non-fast-forward") ||
		strings.Contains(lower, "fetch first") ||
		strings.Contains(lower, "failed to push some refs")
}

func writeAskpassScript(user, token string) (string, error) {
	f, err := os.CreateTemp("", "mdwiki-git-askpass-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%%s\\n' %q ;;\n  *) printf '%%s\\n' %q ;;\nesac\n", user, token)
	if _, err := f.WriteString(script); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := os.Chmod(f.Name(), 0o700); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func appendJobIDTrailer(message, jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return message
	}
	msg := strings.TrimRight(message, "\n")
	if strings.Contains(msg, "\n\n") {
		return msg + "\n" + jobIDTrailerPrefix + jobID
	}
	return msg + "\n\n" + jobIDTrailerPrefix + jobID
}
