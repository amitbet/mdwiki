package comments

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHashBodyAndPageKeyAreDeterministic(t *testing.T) {
	if got, want := HashBody("amit", "hello"), HashBody("amit", "hello"); got != want {
		t.Fatalf("HashBody not deterministic")
	}
	if HashBody("amit", "hello") == HashBody("amit", "goodbye") {
		t.Fatalf("HashBody should differ for different bodies")
	}
	if PageKey("docs/page.md") != PageKey("docs/page.md") {
		t.Fatalf("PageKey not deterministic")
	}
}

func TestNewMessageAndMarshalThread(t *testing.T) {
	msg := NewMessage("amit", "hello", 7)
	if msg.AuthorID != "amit" || msg.Body != "hello" || msg.Position != 7 {
		t.Fatalf("NewMessage returned unexpected data: %+v", msg)
	}
	if _, err := time.Parse(time.RFC3339, msg.CreatedAt); err != nil {
		t.Fatalf("CreatedAt not RFC3339: %v", err)
	}
	if msg.HashID == "" {
		t.Fatalf("HashID should not be empty")
	}

	payload, err := MarshalThread("thread-1", "anchor-1", ThreadFile{ThreadID: "thread-1", Status: "open", Messages: []MessageEntry{msg}})
	if err != nil {
		t.Fatalf("MarshalThread unexpected error: %v", err)
	}
	var decoded ThreadFile
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.SchemaVersion != 1 || decoded.ThreadID != "thread-1" || decoded.AnchorID != "anchor-1" {
		t.Fatalf("decoded thread metadata mismatch: %+v", decoded)
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Body != "hello" {
		t.Fatalf("decoded messages mismatch: %+v", decoded.Messages)
	}
}
