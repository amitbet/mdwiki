package indexbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestScanMarkdownSkipsMdwikiAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mdwiki"), 0o755); err != nil {
		t.Fatalf("mkdir .mdwiki: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Home\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.MD"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.txt"), []byte("ignore\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mdwiki", "hidden.md"), []byte("# Hidden\n"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}

	doc, err := ScanMarkdown(root, "main")
	if err != nil {
		t.Fatalf("ScanMarkdown unexpected error: %v", err)
	}
	if doc.SchemaVersion != 1 || doc.SpaceID != "main" {
		t.Fatalf("unexpected doc metadata: %+v", doc)
	}
	if len(doc.Pages) != 2 {
		t.Fatalf("expected 2 pages, got %d: %+v", len(doc.Pages), doc.Pages)
	}
	paths := map[string]string{}
	for _, p := range doc.Pages {
		paths[p.Path] = p.Title
		if p.PageID == "" {
			t.Fatalf("PageID should not be empty: %+v", p)
		}
	}
	if paths["README.md"] != "README" || paths["docs/guide.MD"] != "guide.MD" {
		t.Fatalf("unexpected paths/titles: %+v", paths)
	}
}

func TestWriteIndexWritesJsonFile(t *testing.T) {
	root := t.TempDir()
	doc := &IndexDoc{
		SchemaVersion: 1,
		SpaceID:       "main",
		Pages: []PageRow{
			{PageID: "p1", Path: "README.md", Title: "README"},
		},
	}
	if err := WriteIndex(root, doc); err != nil {
		t.Fatalf("WriteIndex unexpected error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".mdwiki", "index.json"))
	if err != nil {
		t.Fatalf("ReadFile unexpected error: %v", err)
	}
	var decoded IndexDoc
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal unexpected error: %v", err)
	}
	if decoded.SpaceID != "main" || len(decoded.Pages) != 1 || decoded.Pages[0].Path != "README.md" {
		t.Fatalf("decoded index mismatch: %+v", decoded)
	}
}
