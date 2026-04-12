package comments

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteThreadAndDeleteThread(t *testing.T) {
	root := t.TempDir()
	pageKey := PageKey("docs/page.md")
	threadID := "thread-123"
	thread := ThreadFile{
		ThreadID: threadID,
		Status:   "open",
		Messages: []MessageEntry{
			NewMessage("amit", "First comment", 1),
		},
	}

	if err := WriteThread(root, pageKey, threadID, "anchor-123", thread); err != nil {
		t.Fatalf("WriteThread unexpected error: %v", err)
	}

	path := filepath.Join(root, ".mdwiki", "comments", pageKey, threadID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read thread file: %v", err)
	}

	var got ThreadFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal thread file: %v", err)
	}
	if got.ThreadID != threadID || got.AnchorID != "anchor-123" || got.SchemaVersion != 1 {
		t.Fatalf("unexpected thread metadata: %+v", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].Body != "First comment" {
		t.Fatalf("unexpected thread messages: %+v", got.Messages)
	}

	if err := DeleteThread(root, pageKey, threadID); err != nil {
		t.Fatalf("DeleteThread unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("thread file should be deleted, err=%v", err)
	}
}
