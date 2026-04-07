package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/comments"
)

type addCommentBody struct {
	Path     string `json:"path"`
	AnchorID string `json:"anchor_id"`
	Comment  string `json:"comment"`
	Position int    `json:"position"`
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
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

	authorID := "local"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Login) != "" {
				authorID = strings.TrimSpace(sess.Login)
			}
		}
	}

	threadID := body.AnchorID
	pageKey := comments.PageKey(body.Path)
	msg := comments.NewMessage(authorID, body.Comment, body.Position)
	tf := comments.ThreadFile{
		Status:   "open",
		Messages: []comments.MessageEntry{msg},
	}
	if err := comments.WriteThread(root, pageKey, threadID, body.AnchorID, tf); err != nil {
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
