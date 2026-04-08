package search

import (
	"path/filepath"
	"testing"
)

func TestOpenUpsertAndSearch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "search", "main.sqlite")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open unexpected error: %v", err)
	}
	defer conn.Close()

	if err := UpsertPage(conn, "README.md", "Home", "hello collaborative wiki world"); err != nil {
		t.Fatalf("UpsertPage unexpected error: %v", err)
	}
	if err := UpsertPage(conn, "docs/guide.md", "Guide", "drawio excalidraw image support"); err != nil {
		t.Fatalf("UpsertPage unexpected error: %v", err)
	}

	hits, err := Search(conn, "collaborative", 10)
	if err != nil {
		t.Fatalf("Search unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "README.md" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	if hits[0].Snippet == "" {
		t.Fatalf("expected snippet to be populated")
	}

	hits, err = Search(conn, "drawio", 0)
	if err != nil {
		t.Fatalf("Search unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "Guide" {
		t.Fatalf("unexpected hits with default limit: %+v", hits)
	}

	if err := UpsertPage(conn, "README.md", "Home", "updated body only"); err != nil {
		t.Fatalf("UpsertPage update unexpected error: %v", err)
	}
	hits, err = Search(conn, "collaborative", 10)
	if err != nil {
		t.Fatalf("Search unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected old body to be replaced, got hits: %+v", hits)
	}
}
