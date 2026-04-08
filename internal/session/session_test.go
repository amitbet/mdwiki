package session

import (
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestNewIDLooksHexAndUnique(t *testing.T) {
	a := NewID()
	b := NewID()
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("expected 32-char ids, got %q and %q", a, b)
	}
	if a == b {
		t.Fatalf("expected ids to differ")
	}
}

func TestStorePutGetDelete(t *testing.T) {
	s := NewStore()
	sess := &Session{ID: "abc", Login: "amit"}
	s.Put(sess)
	got, ok := s.Get("abc")
	if !ok || got != sess {
		t.Fatalf("Get mismatch: ok=%v got=%+v", ok, got)
	}
	s.Delete("abc")
	if _, ok := s.Get("abc"); ok {
		t.Fatalf("expected session to be deleted")
	}
}

func TestFromOAuthUsesFallbackNameAndCopiesToken(t *testing.T) {
	expiry := time.Now().Add(1 * time.Hour).UTC()
	sess := FromOAuth("id1", struct {
		ID        int64
		Login     string
		Name      string
		AvatarURL string
	}{
		ID:        42,
		Login:     "amitbet",
		Name:      "",
		AvatarURL: "https://example.invalid/avatar.png",
	}, &oauth2.Token{
		AccessToken: "tok123",
		Expiry:      expiry,
	})
	if sess.Name != "amitbet" {
		t.Fatalf("expected fallback Name to use Login, got %q", sess.Name)
	}
	if sess.AccessToken != "tok123" || !sess.TokenExpiry.Equal(expiry) || sess.GitHubUserID != 42 {
		t.Fatalf("unexpected session from oauth: %+v", sess)
	}
}
