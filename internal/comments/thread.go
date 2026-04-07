package comments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// ThreadFile matches schemas/comment-thread.schema.json (subset).
type ThreadFile struct {
	SchemaVersion int            `json:"schema_version"`
	ThreadID      string         `json:"thread_id"`
	AnchorID      string         `json:"anchor_id"`
	Status        string         `json:"status,omitempty"`
	Messages      []MessageEntry `json:"messages"`
}

// MessageEntry is one comment version in the stack.
type MessageEntry struct {
	HashID    string  `json:"hash_id"`
	Position  int     `json:"position"`
	AuthorID  string  `json:"author_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Body      string  `json:"body"`
	Replaces  *string `json:"replaces,omitempty"`
	InReplyTo *string `json:"in_reply_to,omitempty"`
}

// HashBody returns SHA-256 hex of canonical body for hashing.
func HashBody(authorID, body string) string {
	h := sha256.Sum256([]byte(authorID + "\n" + body))
	return hex.EncodeToString(h[:])
}

// NewMessage creates a message entry with timestamps (UTC RFC3339).
func NewMessage(authorID, body string, position int) MessageEntry {
	now := time.Now().UTC().Format(time.RFC3339)
	hash := HashBody(authorID, body)
	return MessageEntry{
		HashID:    hash,
		Position:  position,
		AuthorID:  authorID,
		CreatedAt: now,
		UpdatedAt: now,
		Body:      body,
	}
}

// PageKey returns a stable key for a page path.
func PageKey(pagePath string) string {
	h := sha256.Sum256([]byte(pagePath))
	return hex.EncodeToString(h[:16])
}

// WriteThread writes `.mdwiki/comments/<pageKey>/<threadID>.json`.
func WriteThread(spaceRoot, pageKey, threadID, anchorID string, tf ThreadFile) error {
	dir := filepath.Join(spaceRoot, ".mdwiki", "comments", pageKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tf.SchemaVersion = 1
	if tf.ThreadID == "" {
		tf.ThreadID = uuid.NewString()
	}
	tf.AnchorID = anchorID
	b, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, threadID+".json")
	return os.WriteFile(p, b, 0o644)
}

// DeleteThread removes a thread file (resolve).
func DeleteThread(spaceRoot, pageKey, threadID string) error {
	p := filepath.Join(spaceRoot, ".mdwiki", "comments", pageKey, threadID+".json")
	return os.Remove(p)
}
