package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Session holds authenticated user state.
type Session struct {
	ID           string
	Login        string
	Name         string
	GitHubUserID int64
	AccessToken  string
	TokenExpiry  time.Time
	AvatarURL    string
}

// Store is an in-memory session map (replace with Redis/DB in production).
type Store struct {
	mu   sync.RWMutex
	data map[string]*Session
}

func NewStore() *Store {
	return &Store{data: make(map[string]*Session)}
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) Put(sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[sess.ID] = sess
}

func (s *Store) Get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.data[id]
	return sess, ok
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
}

// FromOAuth creates session from GitHub OAuth token.
func FromOAuth(id string, u struct {
	ID        int64
	Login     string
	Name      string
	AvatarURL string
}, tok *oauth2.Token) *Session {
	name := u.Name
	if name == "" {
		name = u.Login
	}
	return &Session{
		ID:           id,
		Login:        u.Login,
		Name:         name,
		GitHubUserID: u.ID,
		AccessToken:  tok.AccessToken,
		TokenExpiry:  tok.Expiry,
		AvatarURL:    u.AvatarURL,
	}
}
