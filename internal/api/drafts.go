package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/gitops"
	"mdwiki/internal/session"
)

type draftRecord struct {
	User       string `json:"user"`
	Space      string `json:"space"`
	Path       string `json:"path"`
	BaseCommit string `json:"base_commit"`
	UpdatedAt  string `json:"updated_at"`
	Format     string `json:"format"`
	UpdateB64  string `json:"update_b64"`
	Markdown   string `json:"markdown"`
}

type draftResponse struct {
	Exists            bool   `json:"exists"`
	User              string `json:"user,omitempty"`
	Space             string `json:"space,omitempty"`
	Path              string `json:"path,omitempty"`
	BaseCommit        string `json:"base_commit,omitempty"`
	CurrentBaseCommit string `json:"current_base_commit,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	Format            string `json:"format,omitempty"`
	UpdateB64         string `json:"update_b64,omitempty"`
	Markdown          string `json:"markdown,omitempty"`
	BaseChanged       bool   `json:"base_changed,omitempty"`
}

type saveDraftBody struct {
	Path       string `json:"path"`
	BaseCommit string `json:"base_commit"`
	Format     string `json:"format"`
	UpdateB64  string `json:"update_b64"`
	Markdown   string `json:"markdown"`
}

func draftOwnerFromRequest(s *Server, r *http.Request) string {
	owner := strings.TrimSpace(actorFromRequest(s, r))
	if owner == "" {
		return "local"
	}
	return owner
}

func draftPathHash(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:16])
}

func draftRelPath(user, spaceKey, pagePath string) string {
	return filepath.ToSlash(filepath.Join(".mdwiki", "drafts", "users", user, spaceKey, draftPathHash(pagePath)+".json"))
}

func draftCommitLabel(relPath string) string {
	trimmed := strings.TrimPrefix(relPath, ".mdwiki/drafts/")
	return trimmed
}

func (s *Server) getDraft(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	pagePath, err := normalizeMarkdownRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	owner := draftOwnerFromRequest(s, r)
	repoRoot, err := repoRootForSpace(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rel := draftRelPath(owner, spaceKey, pagePath)
	currentBaseCommit := ""
	if pageRepoRoot, pageRepoRelPath, mapErr := resolveRepoPath(root, pagePath); mapErr == nil {
		currentBaseCommit, _ = gitops.LastCommitForPath(pageRepoRoot, pageRepoRelPath)
	}
	b, err := gitops.ReadFile(repoRoot, rel)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(draftResponse{Exists: false, CurrentBaseCommit: currentBaseCommit})
		return
	}
	var rec draftRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(draftResponse{
		Exists:            true,
		User:              rec.User,
		Space:             rec.Space,
		Path:              rec.Path,
		BaseCommit:        rec.BaseCommit,
		CurrentBaseCommit: currentBaseCommit,
		UpdatedAt:         rec.UpdatedAt,
		Format:            rec.Format,
		UpdateB64:         rec.UpdateB64,
		Markdown:          rec.Markdown,
		BaseChanged:       rec.BaseCommit != "" && currentBaseCommit != "" && rec.BaseCommit != currentBaseCommit,
	})
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	var body saveDraftBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Path, err = normalizeMarkdownRelPath(body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Markdown) == "" {
		http.Error(w, "markdown is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Format) == "" {
		body.Format = "yjs"
	}
	owner := draftOwnerFromRequest(s, r)
	repoRoot, err := repoRootForSpace(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pageRepoRoot, pageRepoRelPath, err := resolveRepoPath(root, body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	baseCommit := strings.TrimSpace(body.BaseCommit)
	if baseCommit == "" {
		baseCommit, _ = gitops.LastCommitForPath(pageRepoRoot, pageRepoRelPath)
	}
	rec := draftRecord{
		User:       owner,
		Space:      spaceKey,
		Path:       body.Path,
		BaseCommit: baseCommit,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Format:     body.Format,
		UpdateB64:  strings.TrimSpace(body.UpdateB64),
		Markdown:   body.Markdown,
	}
	payload, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payload = append(payload, '\n')
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		cfg, cfgErr := s.loadSettings(r.Context())
		if cfgErr != nil {
			http.Error(w, cfgErr.Error(), http.StatusInternalServerError)
			return
		}
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	rel := draftRelPath(owner, spaceKey, body.Path)
	authorName, authorEmail := authorFromRequest(s, r)
	_, err = s.executeGitWrite(r.Context(), gitWriteJob{
		ID:          session.NewID(),
		Op:          "save",
		RepoRoot:    repoRoot,
		Branch:      branch,
		Path:        rel,
		Content:     string(payload),
		CommitMsg:   fmt.Sprintf("draft: save %s", draftCommitLabel(rel)),
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		PushUser:    s.pushAuthUsername(r),
		PushToken:   s.pushToken(r),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "updated_at": rec.UpdatedAt, "base_commit": rec.BaseCommit})
}

func (s *Server) deleteDraft(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	pagePath, err := normalizeMarkdownRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	owner := draftOwnerFromRequest(s, r)
	err = s.deleteDraftForOwnerPath(r.Context(), r, spaceKey, root, ent.Branch, owner, pagePath)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "pathspec") && !strings.Contains(strings.ToLower(err.Error()), "did not match any files") {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) deleteDraftForPath(ctx context.Context, r *http.Request, spaceKey, root, branch, pagePath string) error {
	return s.deleteDraftForOwnerPath(ctx, r, spaceKey, root, branch, draftOwnerFromRequest(s, r), pagePath)
}

func (s *Server) deleteDraftForOwnerPath(ctx context.Context, r *http.Request, spaceKey, root, branch, owner, pagePath string) error {
	repoRoot, err := repoRootForSpace(root)
	if err != nil {
		return err
	}
	resolvedBranch := strings.TrimSpace(branch)
	if resolvedBranch == "" {
		cfg, cfgErr := s.loadSettings(ctx)
		if cfgErr != nil {
			return cfgErr
		}
		resolvedBranch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if resolvedBranch == "" {
		resolvedBranch = "main"
	}
	rel := draftRelPath(owner, spaceKey, pagePath)
	authorName, authorEmail := authorFromRequest(s, r)
	_, err = s.executeGitWrite(ctx, gitWriteJob{
		ID:          session.NewID(),
		Op:          "delete",
		RepoRoot:    repoRoot,
		Branch:      resolvedBranch,
		Path:        rel,
		CommitMsg:   fmt.Sprintf("draft: remove %s", draftCommitLabel(rel)),
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		PushUser:    s.pushAuthUsername(r),
		PushToken:   s.pushToken(r),
	})
	return err
}
