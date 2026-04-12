package api

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"mdwiki/internal/gitops"
	wshub "mdwiki/internal/ws"
)

const (
	redisGitStreamKey   = "mdwiki:git:{queue}:stream"
	redisGitGroup       = "mdwiki-git-workers"
	redisGitLockKey     = "mdwiki:git:{queue}:lock"
	redisGitJobStateTTL = 24 * time.Hour
)

type gitWriteJob struct {
	ID          string   `json:"id"`
	Op          string   `json:"op"`
	RepoRoot    string   `json:"repo_root"`
	Branch      string   `json:"branch"`
	Path        string   `json:"path,omitempty"`
	NotifySpace string   `json:"notify_space,omitempty"`
	NotifyPath  string   `json:"notify_path,omitempty"`
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

type gitJobState struct {
	Status    string         `json:"status"`
	JobID     string         `json:"job_id"`
	Path      string         `json:"path,omitempty"`
	Result    gitWriteResult `json:"result,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type gitQueueAcceptResult struct {
	JobID string
	Path  string
}

func (s *Server) executeGitWrite(ctx context.Context, job gitWriteJob) (gitWriteResult, error) {
	if s.Redis == nil {
		return s.executeGitWriteLocal(job), nil
	}
	accepted, err := s.enqueueGitWrite(ctx, job)
	if err != nil {
		return gitWriteResult{}, err
	}
	out, err := s.waitForGitJobResult(ctx, accepted.JobID, 45*time.Second)
	if err != nil {
		return gitWriteResult{}, err
	}
	if !out.OK && strings.TrimSpace(out.Error) != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}

func (s *Server) enqueueGitWrite(ctx context.Context, job gitWriteJob) (gitQueueAcceptResult, error) {
	if s.Redis == nil {
		return gitQueueAcceptResult{}, errors.New("redis unavailable")
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = uuid.NewString()
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return gitQueueAcceptResult{}, err
	}
	jobKey := s.redisGitJobStateKey(job.ID)
	queuedState := gitJobState{Status: "queued", JobID: job.ID, Path: gitJobPath(job), UpdatedAt: time.Now().UTC()}
	queuedPayload, err := json.Marshal(queuedState)
	if err != nil {
		return gitQueueAcceptResult{}, err
	}
	enqueued, err := s.Redis.EnqueueStreamOnce(ctx, redisGitStreamKey, jobKey, queuedPayload, redisGitJobStateTTL, payload)
	if err != nil {
		return gitQueueAcceptResult{}, err
	}
	if !enqueued {
		return gitQueueAcceptResult{JobID: job.ID, Path: job.Path}, nil
	}
	if err := s.storeGitJobState(ctx, job.ID, queuedState); err != nil {
		return gitQueueAcceptResult{}, err
	}
	s.broadcastGitJobUpdate(queuedState)
	return gitQueueAcceptResult{JobID: job.ID, Path: job.Path}, nil
}

func (s *Server) executeGitWriteLocal(job gitWriteJob) gitWriteResult {
	s.gitWriteMu.Lock()
	defer s.gitWriteMu.Unlock()
	return runGitWriteJob(context.Background(), job)
}

func (s *Server) runRedisGitQueueWorker(ctx context.Context) {
	consumer := "worker-" + uuid.NewString()
	if err := s.Redis.EnsureConsumerGroup(ctx, redisGitStreamKey, redisGitGroup); err != nil {
		log.Printf("git queue group init: %v", err)
		return
	}
	claimStart := "0-0"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, next, err := s.Redis.AutoClaim(ctx, redisGitStreamKey, redisGitGroup, consumer, claimStart, 30*time.Second, 8)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, redis.Nil) {
				continue
			}
			log.Printf("git queue autoclaim: %v", err)
			continue
		}
		claimStart = next
		if len(msgs) == 0 {
			msgs, err = s.Redis.ReadGroup(ctx, redisGitStreamKey, redisGitGroup, consumer, 2*time.Second, 8)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, redis.Nil) {
					continue
				}
				log.Printf("git queue readgroup: %v", err)
				continue
			}
		}

		for _, msg := range msgs {
			s.runQueuedGitJob(ctx, msg)
		}
	}
}

func (s *Server) runQueuedGitJob(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("git queue missing payload for message %s", msg.ID)
		_ = s.Redis.AckStream(context.Background(), redisGitStreamKey, redisGitGroup, msg.ID)
		return
	}
	var payload []byte
	switch value := raw.(type) {
	case string:
		payload = []byte(value)
	case []byte:
		payload = value
	default:
		payload = []byte(fmt.Sprint(value))
	}

	var job gitWriteJob
	if err := json.Unmarshal(payload, &job); err != nil {
		log.Printf("git queue decode: %v", err)
		_ = s.Redis.AckStream(context.Background(), redisGitStreamKey, redisGitGroup, msg.ID)
		return
	}
	if strings.TrimSpace(job.ID) == "" {
		_ = s.Redis.AckStream(context.Background(), redisGitStreamKey, redisGitGroup, msg.ID)
		return
	}
	if out, done, err := s.loadGitJobState(context.Background(), job.ID); err == nil && done {
		if err := s.Redis.AckStream(context.Background(), redisGitStreamKey, redisGitGroup, msg.ID); err != nil {
			log.Printf("git queue ack completed job: %v", err)
		}
		s.broadcastGitJobUpdate(terminalStateForResult(job, out))
		return
	}

	res, err := s.executeWithRedisGitLock(ctx, job)
	if err != nil {
		res = gitWriteResult{OK: false, Error: err.Error()}
	}

	terminalState := terminalStateForResult(job, res)
	if err := s.storeGitJobState(context.Background(), job.ID, terminalState); err != nil {
		log.Printf("git queue store result: %v", err)
		return
	}
	s.broadcastGitJobUpdate(terminalState)
	if res.OK && job.NotifySpace != "" && job.NotifyPath != "" {
		s.Hub.BroadcastControlToSpace(job.NotifySpace, wshub.Control{
			Type:   wshub.MsgPageSaved,
			Path:   job.NotifyPath,
			Commit: res.Commit,
		})
	}
	if err := s.Redis.AckStream(context.Background(), redisGitStreamKey, redisGitGroup, msg.ID); err != nil {
		log.Printf("git queue ack: %v", err)
	}
}

func (s *Server) executeWithRedisGitLock(ctx context.Context, job gitWriteJob) (gitWriteResult, error) {
	lockOwner := uuid.NewString()
	lockTTL := 5 * time.Minute
	lockKey := s.redisGitLockKey(job)
	for {
		ok, err := s.Redis.TryAcquireLock(ctx, lockKey, lockOwner, lockTTL)
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
	if err := s.storeGitJobState(context.Background(), job.ID, gitJobState{
		Status:    "running",
		JobID:     job.ID,
		Path:      gitJobPath(job),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("git queue mark running: %v", err)
	}
	s.broadcastGitJobUpdate(gitJobState{
		Status:    "running",
		JobID:     job.ID,
		Path:      gitJobPath(job),
		UpdatedAt: time.Now().UTC(),
	})
	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()
	stopRenew := make(chan struct{})
	defer close(stopRenew)
	lostLock := make(chan struct{})
	go s.renewGitLock(lockKey, lockOwner, lockTTL, stopRenew, lostLock)
	defer func() {
		if err := s.Redis.ReleaseLock(context.Background(), lockKey, lockOwner); err != nil {
			log.Printf("git queue unlock: %v", err)
		}
	}()
	go func() {
		select {
		case <-lostLock:
			cancelExec()
		case <-stopRenew:
		}
	}()

	res := runGitWriteJob(execCtx, job)
	select {
	case <-lostLock:
		return gitWriteResult{}, errors.New("redis git lock lost during execution")
	default:
	}
	if !res.OK && strings.TrimSpace(res.Error) != "" {
		return res, errors.New(res.Error)
	}
	return res, nil
}

func runGitWriteJob(ctx context.Context, job gitWriteJob) gitWriteResult {
	existingCommit, err := gitops.FindCommitByJobID(job.RepoRoot, job.Branch, job.ID)
	if err != nil {
		return gitWriteResult{OK: false, Error: err.Error()}
	}
	if existingCommit != "" {
		return resumeAppliedGitJob(ctx, job, existingCommit)
	}

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
		if err := gitops.WriteFileCommitLocalWithJob(ctx, job.RepoRoot, job.Branch, job.Path, content, job.AuthorName, job.AuthorEmail, job.CommitMsg, job.CoAuthors, job.ID); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		pushErr := gitops.PushContext(ctx, job.RepoRoot, job.PushUser, job.PushToken, job.Branch)
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
		if err := gitops.RenameFileLocalWithJob(ctx, job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.AuthorName, job.AuthorEmail, job.ID); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		if err := gitops.PushContext(ctx, job.RepoRoot, job.PushUser, job.PushToken, job.Branch); err != nil {
			log.Printf("git rename push: root=%s branch=%s from=%s to=%s user=%s has_token=%t err=%v", job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.PushUser, strings.TrimSpace(job.PushToken) != "", err)
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		log.Printf("git rename push: root=%s branch=%s from=%s to=%s user=%s has_token=%t err=<nil>", job.RepoRoot, job.Branch, job.FromPath, job.ToPath, job.PushUser, strings.TrimSpace(job.PushToken) != "")
		return gitWriteResult{OK: true, Path: job.ToPath, Commit: GitHeadShort(job.RepoRoot), Message: "Committed and pushed"}

	case "delete":
		if err := gitops.DeleteFileLocalWithJob(ctx, job.RepoRoot, job.Branch, job.Path, job.AuthorName, job.AuthorEmail, job.CommitMsg, job.ID); err != nil {
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		if err := gitops.PushContext(ctx, job.RepoRoot, job.PushUser, job.PushToken, job.Branch); err != nil {
			log.Printf("git delete push: root=%s branch=%s path=%s user=%s has_token=%t err=%v", job.RepoRoot, job.Branch, job.Path, job.PushUser, strings.TrimSpace(job.PushToken) != "", err)
			return gitWriteResult{OK: false, Error: err.Error()}
		}
		log.Printf("git delete push: root=%s branch=%s path=%s user=%s has_token=%t err=<nil>", job.RepoRoot, job.Branch, job.Path, job.PushUser, strings.TrimSpace(job.PushToken) != "")
		return gitWriteResult{OK: true, Path: job.Path, Commit: GitHeadShort(job.RepoRoot), Message: "Committed and pushed"}
	}
	return gitWriteResult{OK: false, Error: fmt.Sprintf("unknown git job op: %s", job.Op)}
}

func (s *Server) waitForGitJobResult(ctx context.Context, jobID string, timeout time.Duration) (gitWriteResult, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		out, done, err := s.loadGitJobState(waitCtx, jobID)
		if err == nil && done {
			return out, nil
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			return gitWriteResult{}, err
		}

		select {
		case <-waitCtx.Done():
			if out, done, err := s.loadGitJobState(context.Background(), jobID); err == nil && done {
				return out, nil
			}
			return gitWriteResult{}, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) loadGitJobState(ctx context.Context, jobID string) (gitWriteResult, bool, error) {
	state, err := s.getGitJobState(ctx, jobID)
	if err != nil {
		return gitWriteResult{}, false, err
	}
	switch state.Status {
	case "succeeded", "failed":
		return state.Result, true, nil
	default:
		return gitWriteResult{}, false, nil
	}
}

func (s *Server) getGitJobState(ctx context.Context, jobID string) (gitJobState, error) {
	payload, err := s.Redis.Get(ctx, s.redisGitJobStateKey(jobID))
	if err != nil {
		return gitJobState{}, err
	}
	var state gitJobState
	if err := json.Unmarshal(payload, &state); err != nil {
		return gitJobState{}, err
	}
	return state, nil
}

func (s *Server) storeGitJobState(ctx context.Context, jobID string, state gitJobState) error {
	state.JobID = jobID
	state.UpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.Redis.Set(ctx, s.redisGitJobStateKey(jobID), payload, redisGitJobStateTTL)
}

func terminalStateForResult(job gitWriteJob, res gitWriteResult) gitJobState {
	status := "succeeded"
	if !res.OK {
		status = "failed"
	}
	return gitJobState{
		Status: status,
		JobID:  job.ID,
		Path:   gitJobPath(job),
		Result: res,
	}
}

func (s *Server) renewGitLock(lockKey, owner string, ttl time.Duration, stop <-chan struct{}, lostLock chan struct{}) {
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	lastConfirmed := time.Now()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now()
			ok, err := s.Redis.RenewLock(context.Background(), lockKey, owner, ttl)
			if err != nil {
				log.Printf("git queue renew lock: %v", err)
				if renewErrorForfeitsLock(lastConfirmed, now, ttl) {
					log.Printf("git queue renew lock giving up after TTL elapsed: %s", lockKey)
					close(lostLock)
					return
				}
				continue
			}
			if !ok {
				log.Printf("git queue renew lock lost ownership: %s", lockKey)
				close(lostLock)
				return
			}
			lastConfirmed = now
		}
	}
}

func (s *Server) redisGitJobStateKey(jobID string) string {
	return "mdwiki:git:{queue}:job:" + jobID
}

func (s *Server) redisGitLockKey(job gitWriteJob) string {
	sum := sha1.Sum([]byte(job.RepoRoot + "\n" + job.Branch))
	return fmt.Sprintf("%s:%x", redisGitLockKey, sum[:8])
}

func (s *Server) broadcastGitJobUpdate(state gitJobState) {
	s.Hub.BroadcastControlAll(wshub.Control{
		Type:    wshub.MsgGitJobUpdate,
		JobID:   state.JobID,
		Path:    state.Path,
		Status:  state.Status,
		Commit:  state.Result.Commit,
		Message: state.Result.Message,
		Error:   state.Result.Error,
	})
}

func gitJobPath(job gitWriteJob) string {
	if strings.TrimSpace(job.NotifyPath) != "" {
		return job.NotifyPath
	}
	return job.Path
}

func resumeAppliedGitJob(ctx context.Context, job gitWriteJob, commit string) gitWriteResult {
	pushErr := gitops.PushContext(ctx, job.RepoRoot, job.PushUser, job.PushToken, job.Branch)
	shortCommit := shortCommitHash(commit)
	switch job.Op {
	case "save":
		msg := "Already applied and pushed"
		if pushErr != nil {
			msg = "Already committed locally; push failed: " + pushErr.Error()
		}
		return gitWriteResult{OK: true, Path: job.Path, Commit: shortCommit, Message: msg}
	case "rename":
		if pushErr != nil {
			return gitWriteResult{OK: false, Error: pushErr.Error()}
		}
		return gitWriteResult{OK: true, Path: job.ToPath, Commit: shortCommit, Message: "Already applied and pushed"}
	case "delete":
		if pushErr != nil {
			return gitWriteResult{OK: false, Error: pushErr.Error()}
		}
		return gitWriteResult{OK: true, Path: job.Path, Commit: shortCommit, Message: "Already applied and pushed"}
	default:
		if pushErr != nil {
			return gitWriteResult{OK: false, Error: pushErr.Error()}
		}
		return gitWriteResult{OK: true, Commit: shortCommit, Message: "Already applied and pushed"}
	}
}

func shortCommitHash(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func renewErrorForfeitsLock(lastConfirmed, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		return true
	}
	if now.Before(lastConfirmed) {
		return false
	}
	return now.Sub(lastConfirmed) >= ttl
}
