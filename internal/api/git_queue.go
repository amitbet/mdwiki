package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"mdwiki/internal/gitops"
)

const (
	redisGitQueueKey = "mdwiki:git:queue"
	redisGitLockKey  = "mdwiki:git:lock"
)

type gitWriteJob struct {
	ID          string   `json:"id"`
	Op          string   `json:"op"`
	RepoRoot    string   `json:"repo_root"`
	Branch      string   `json:"branch"`
	Path        string   `json:"path,omitempty"`
	Content     string   `json:"content,omitempty"`
	ContentB64  string   `json:"content_b64,omitempty"`
	FromPath    string   `json:"from_path,omitempty"`
	ToPath      string   `json:"to_path,omitempty"`
	CommitMsg   string   `json:"commit_msg,omitempty"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	PushUser    string   `json:"push_user"`
	PushToken   string   `json:"push_token"`
	CoAuthors   []string `json:"co_authors,omitempty"`
	StrictPush  bool     `json:"strict_push"`
}

type gitWriteResult struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) executeGitWrite(ctx context.Context, job gitWriteJob) (gitWriteResult, error) {
	if s.Redis == nil {
		return s.executeGitWriteLocal(job), nil
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = uuid.NewString()
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return gitWriteResult{}, err
	}
	if err := s.Redis.Enqueue(ctx, redisGitQueueKey, payload); err != nil {
		return gitWriteResult{}, err
	}
	resultKey := s.redisGitResultKey(job.ID)
	resp, err := s.Redis.WaitResult(ctx, resultKey, 45*time.Second)
	if err != nil {
		return gitWriteResult{}, err
	}
	var out gitWriteResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return gitWriteResult{}, err
	}
	if !out.OK && strings.TrimSpace(out.Error) != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}

func (s *Server) executeGitWriteLocal(job gitWriteJob) gitWriteResult {
	s.gitWriteMu.Lock()
	defer s.gitWriteMu.Unlock()
	return runGitWriteJob(job)
}

func (s *Server) runRedisGitQueueWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, err := s.Redis.DequeueBlocking(ctx, redisGitQueueKey, 2*time.Second)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				continue
			}
			if errors.Is(err, redis.Nil) {
				continue
			}
			log.Printf("git queue dequeue: %v", err)
			continue
		}

		var job gitWriteJob
		if err := json.Unmarshal(raw, &job); err != nil {
			log.Printf("git queue decode: %v", err)
			continue
		}
		s.runQueuedGitJob(ctx, job)
	}
}

func (s *Server) runQueuedGitJob(ctx context.Context, job gitWriteJob) {
	if strings.TrimSpace(job.ID) == "" {
		return
	}
	resultKey := s.redisGitResultKey(job.ID)

	res, err := s.executeWithRedisGitLock(ctx, job)
	if err != nil {
		res = gitWriteResult{OK: false, Error: err.Error()}
	}

	payload, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		payload = []byte(`{"ok":false,"error":"failed to encode result"}`)
	}
	if err := s.Redis.PublishResult(ctx, resultKey, payload, 5*time.Minute); err != nil {
		log.Printf("git queue publish result: %v", err)
	}
}

func (s *Server) executeWithRedisGitLock(ctx context.Context, job gitWriteJob) (gitWriteResult, error) {
	lockOwner := uuid.NewString()
	lockTTL := 5 * time.Minute
	for {
		ok, err := s.Redis.TryAcquireLock(ctx, redisGitLockKey, lockOwner, lockTTL)
		if err != nil {
			return gitWriteResult{}, err
		}
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return gitWriteResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer func() {
		if err := s.Redis.ReleaseLock(context.Background(), redisGitLockKey, lockOwner); err != nil {
			log.Printf("git queue unlock: %v", err)
		}
	}()

	res := runGitWriteJob(job)
	if !res.OK && strings.TrimSpace(res.Error) != "" {
		return res, errors.New(res.Error)
	}
	return res, nil
}

func runGitWriteJob(job gitWriteJob) gitWriteResult {
	switch job.Op {
	case "save":
		content := []byte(job.Content)
		if strings.TrimSpace(job.ContentB64) != "" {
			decoded, err := gitops.DecodeBytesBase64(job.ContentB64)
			if err != nil {
				return gitWriteResult{OK: false, Error: err.Error()}
			}
			content = decoded
		}
		if err := gitops.WriteFileCommitLocal(job.RepoRoot, job.Branch, job.Path, content, job.AuthorName, job.AuthorEmail, job.CommitMsg, job.CoAuthors); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		pushErr := gitops.Push(job.RepoRoot, job.PushUser, job.PushToken, job.Branch)
		log.Printf("git save push: root=%s branch=%s path=%s user=%s has_token=%t err=%v", job.RepoRoot, job.Branch, job.Path, job.PushUser, strings.TrimSpace(job.PushToken) != "", pushErr)
		msg := "Committed and pushed"
		if pushErr != nil {
			msg = "Committed locally; push failed: " + pushErr.Error()
		}
		return gitWriteResult{
			OK:      true,
			Path:    job.Path,
			Commit:  GitHeadShort(job.RepoRoot),
			Message: msg,
		}

	case "rename":
		if err := gitops.RenameFileLocal(job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.AuthorName, job.AuthorEmail); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		if err := gitops.Push(job.RepoRoot, job.PushUser, job.PushToken, job.Branch); err != nil {
			log.Printf("git rename push: root=%s branch=%s from=%s to=%s user=%s has_token=%t err=%v", job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.PushUser, strings.TrimSpace(job.PushToken) != "", err)
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		log.Printf("git rename push: root=%s branch=%s from=%s to=%s user=%s has_token=%t err=<nil>", job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.PushUser, strings.TrimSpace(job.PushToken) != "")
		return gitWriteResult{OK: true, Path: job.ToPath, Message: "Committed and pushed"}

	case "delete":
		if err := gitops.DeleteFileLocalWithMessage(job.RepoRoot, job.Branch, job.Path, job.AuthorName, job.AuthorEmail, job.CommitMsg); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		if err := gitops.Push(job.RepoRoot, job.PushUser, job.PushToken, job.Branch); err != nil {
			log.Printf("git delete push: root=%s branch=%s path=%s user=%s has_token=%t err=%v", job.RepoRoot, job.Branch, job.Path, job.PushUser, strings.TrimSpace(job.PushToken) != "", err)
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		log.Printf("git delete push: root=%s branch=%s path=%s user=%s has_token=%t err=<nil>", job.RepoRoot, job.Branch, job.Path, job.PushUser, strings.TrimSpace(job.PushToken) != "")
		return gitWriteResult{OK: true, Path: job.Path, Message: "Committed and pushed"}
	}
	return gitWriteResult{OK: false, Error: fmt.Sprintf("unknown git job op: %s", job.Op)}
}

func (s *Server) redisGitResultKey(jobID string) string {
	return "mdwiki:git:result:" + jobID
}
