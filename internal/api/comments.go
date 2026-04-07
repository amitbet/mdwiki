package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/comments"
	"mdwiki/internal/gitops"
	"mdwiki/internal/session"
)

type addCommentBody struct {
	Path     string `json:"path"`
	AnchorID string `json:"anchor_id"`
	Comment  string `json:"comment"`
	Position int    `json:"position"`
}

type updateCommentBody struct {
	Path      string `json:"path"`
	Comment   string `json:"comment"`
	Position  int    `json:"position"`
	HashID    string `json:"hash_id,omitempty"`
	InReplyTo string `json:"in_reply_to,omitempty"`
}

type listCommentMessage struct {
	HashID    string  `json:"hash_id"`
	Position  int     `json:"position"`
	AuthorID  string  `json:"author_id"`
	Body      string  `json:"body"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Replaces  *string `json:"replaces,omitempty"`
	InReplyTo *string `json:"in_reply_to,omitempty"`
	CanEdit   bool    `json:"can_edit"`
}

type listComment struct {
	AnchorID string               `json:"anchor_id"`
	ThreadID string               `json:"thread_id"`
	Status   string               `json:"status,omitempty"`
	Messages []listCommentMessage `json:"messages"`
}

func actorFromRequest(s *Server, r *http.Request) string {
	sid := sessionFromCookie(r)
	if sid == "" {
		return "local"
	}
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		return "local"
	}
	if strings.TrimSpace(sess.Login) == "" {
		return "local"
	}
	return strings.TrimSpace(sess.Login)
}

func authorFromRequest(s *Server, r *http.Request) (string, string) {
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	sid := sessionFromCookie(r)
	if sid == "" {
		return authorName, authorEmail
	}
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		return authorName, authorEmail
	}
	if strings.TrimSpace(sess.Name) != "" {
		authorName = sess.Name
	} else if strings.TrimSpace(sess.Login) != "" {
		authorName = sess.Login
	}
	if strings.TrimSpace(sess.Login) != "" {
		authorEmail = sess.Login + "@users.noreply.github.com"
	}
	return authorName, authorEmail
}

func (s *Server) writeCommentThread(r *http.Request, root, branch, pageKey, threadID, anchorID string, tf comments.ThreadFile) error {
	b, err := comments.MarshalThread(threadID, anchorID, tf)
	if err != nil {
		return err
	}
	relPath := filepath.ToSlash(filepath.Join(".mdwiki", "comments", pageKey, threadID+".json"))
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		return err
	}
	if normalizeSaveMode(cfg.SaveMode) == "local" {
		return gitops.WriteFileOnly(root, relPath, string(b))
	}
	repoRoot, repoRelPath, err := resolveRepoPath(root, relPath)
	if err != nil {
		return err
	}
	authorName, authorEmail := authorFromRequest(s, r)
	_, err = s.executeGitWrite(r.Context(), gitWriteJob{
		ID:          session.NewID(),
		Op:          "save",
		RepoRoot:    repoRoot,
		Branch:      branch,
		Path:        repoRelPath,
		Content:     string(b),
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		PushUser:    s.pushAuthUsername(r),
		PushToken:   s.pushToken(r),
	})
	return err
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
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
	pagePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if pagePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	pageKey := comments.PageKey(pagePath)
	dir := filepath.Join(root, ".mdwiki", "comments", pageKey)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			_ = json.NewEncoder(w).Encode(map[string]any{"comments": []listComment{}})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actor := actorFromRequest(s, r)
	out := make([]listComment, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		var tf comments.ThreadFile
		if err := json.Unmarshal(b, &tf); err != nil {
			continue
		}
		if len(tf.Messages) == 0 {
			continue
		}
		threadID := strings.TrimSuffix(ent.Name(), ".json")
		msgs := make([]listCommentMessage, 0, len(tf.Messages))
		for _, m := range tf.Messages {
			msgs = append(msgs, listCommentMessage{
				HashID:    m.HashID,
				Position:  m.Position,
				AuthorID:  m.AuthorID,
				Body:      m.Body,
				CreatedAt: m.CreatedAt,
				UpdatedAt: m.UpdatedAt,
				Replaces:  m.Replaces,
				InReplyTo: m.InReplyTo,
				CanEdit:   actor == m.AuthorID,
			})
		}
		out = append(out, listComment{
			AnchorID: tf.AnchorID,
			ThreadID: threadID,
			Status:   tf.Status,
			Messages: msgs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AnchorID < out[j].AnchorID })
	_ = json.NewEncoder(w).Encode(map[string]any{"comments": out})
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
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

	var body addCommentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Path = strings.TrimSpace(body.Path)
	body.AnchorID = strings.TrimSpace(body.AnchorID)
	body.Comment = strings.TrimSpace(body.Comment)
	if body.Path == "" || body.AnchorID == "" || body.Comment == "" {
		http.Error(w, "path, anchor_id, and comment are required", http.StatusBadRequest)
		return
	}
	if body.Position < 0 {
		body.Position = 0
	}

	threadID := body.AnchorID
	pageKey := comments.PageKey(body.Path)
	msg := comments.NewMessage(actorFromRequest(s, r), body.Comment, body.Position)
	tf := comments.ThreadFile{
		Status:   "open",
		Messages: []comments.MessageEntry{msg},
	}
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	if err := s.writeCommentThread(r, root, branch, pageKey, threadID, body.AnchorID, tf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"thread_id": threadID,
		"page_key":  pageKey,
		"anchor_id": body.AnchorID,
		"message":   msg,
	})
}

func (s *Server) replyComment(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	threadID := strings.TrimSpace(chi.URLParam(r, "thread"))
	if threadID == "" {
		http.Error(w, "thread is required", http.StatusBadRequest)
		return
	}
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	var body updateCommentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Path = strings.TrimSpace(body.Path)
	body.Comment = strings.TrimSpace(body.Comment)
	if body.Path == "" || body.Comment == "" {
		http.Error(w, "path and comment are required", http.StatusBadRequest)
		return
	}
	if body.Position < 0 {
		body.Position = 0
	}
	pageKey := comments.PageKey(body.Path)
	p := filepath.Join(root, ".mdwiki", "comments", pageKey, threadID+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "thread not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var tf comments.ThreadFile
	if err := json.Unmarshal(b, &tf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msg := comments.NewMessage(actorFromRequest(s, r), body.Comment, body.Position)
	if body.InReplyTo != "" {
		replyTo := body.InReplyTo
		msg.InReplyTo = &replyTo
	}
	tf.Messages = append(tf.Messages, msg)
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	if err := s.writeCommentThread(r, root, branch, pageKey, threadID, tf.AnchorID, tf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": msg})
}

func (s *Server) editComment(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	threadID := strings.TrimSpace(chi.URLParam(r, "thread"))
	if threadID == "" {
		http.Error(w, "thread is required", http.StatusBadRequest)
		return
	}
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	var body updateCommentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Path = strings.TrimSpace(body.Path)
	body.Comment = strings.TrimSpace(body.Comment)
	body.HashID = strings.TrimSpace(body.HashID)
	if body.Path == "" || body.Comment == "" || body.HashID == "" {
		http.Error(w, "path, hash_id and comment are required", http.StatusBadRequest)
		return
	}
	if body.Position < 0 {
		body.Position = 0
	}
	pageKey := comments.PageKey(body.Path)
	p := filepath.Join(root, ".mdwiki", "comments", pageKey, threadID+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "thread not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var tf comments.ThreadFile
	if err := json.Unmarshal(b, &tf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actor := actorFromRequest(s, r)
	var target *comments.MessageEntry
	for i := range tf.Messages {
		if tf.Messages[i].HashID == body.HashID {
			target = &tf.Messages[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	if target.AuthorID != actor {
		http.Error(w, "forbidden: only owner can edit", http.StatusForbidden)
		return
	}

	msg := comments.NewMessage(actor, body.Comment, body.Position)
	replaces := target.HashID
	msg.Replaces = &replaces
	msg.InReplyTo = target.InReplyTo
	tf.Messages = append(tf.Messages, msg)
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	if err := s.writeCommentThread(r, root, branch, pageKey, threadID, tf.AnchorID, tf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": msg})
}

func (s *Server) resolveComment(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	threadID := strings.TrimSpace(chi.URLParam(r, "thread"))
	if threadID == "" {
		http.Error(w, "thread is required", http.StatusBadRequest)
		return
	}
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	var body updateCommentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Path = strings.TrimSpace(body.Path)
	if body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	pageKey := comments.PageKey(body.Path)
	p := filepath.Join(root, ".mdwiki", "comments", pageKey, threadID+".json")
	relDelete := filepath.ToSlash(filepath.Join(".mdwiki", "comments", pageKey, threadID+".json"))
	cfg, cfgErr := s.loadSettings(r.Context())
	if cfgErr != nil {
		http.Error(w, cfgErr.Error(), http.StatusInternalServerError)
		return
	}
	saveMode := normalizeSaveMode(cfg.SaveMode)
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Name) != "" {
				authorName = sess.Name
			} else if strings.TrimSpace(sess.Login) != "" {
				authorName = sess.Login
			}
			if strings.TrimSpace(sess.Login) != "" {
				authorEmail = sess.Login + "@users.noreply.github.com"
			}
		}
	}
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}

	if saveMode == "local" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		repoRoot, repoRelPath, mapErr := resolveRepoPath(root, relDelete)
		if mapErr != nil {
			http.Error(w, mapErr.Error(), http.StatusInternalServerError)
			return
		}
		if err := gitops.DeleteFileLocal(repoRoot, branch, repoRelPath, authorName, authorEmail); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := gitops.Push(repoRoot, s.pushAuthUsername(r), s.pushToken(r), branch); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": "resolved", "deleted": true})
}
