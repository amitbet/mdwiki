package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeMarkdownRelPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "README.md", want: "README.md"},
		{in: "docs/page", want: "docs/page.md"},
		{in: " nested/path.MD ", want: "nested/path.MD"},
		{in: "", wantErr: true},
		{in: "/abs.md", wantErr: true},
		{in: "../escape.md", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeMarkdownRelPath(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("normalizeMarkdownRelPath(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeMarkdownRelPath(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeMarkdownRelPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeAssetRelPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "assets/images/pic.png", want: "assets/images/pic.png"},
		{in: ".mdwiki/assets/images/pic.png", want: ".mdwiki/assets/images/pic.png"},
		{in: " /assets/images/pic.png ", wantErr: true},
		{in: "images/pic.png", wantErr: true},
		{in: "assets/../pic.png", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeAssetRelPath(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("normalizeAssetRelPath(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeAssetRelPath(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeAssetRelPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDraftHelpers(t *testing.T) {
	hashA := draftPathHash("docs/page.md")
	hashB := draftPathHash("docs/page.md")
	if hashA != hashB {
		t.Fatalf("draftPathHash should be deterministic: %q != %q", hashA, hashB)
	}
	if hashA == draftPathHash("docs/other.md") {
		t.Fatalf("draftPathHash should differ for different paths")
	}

	rel := draftRelPath("amit", "main", "docs/page.md")
	want := filepath.ToSlash(filepath.Join(".mdwiki", "drafts", "users", "amit", "main", hashA+".json"))
	if rel != want {
		t.Fatalf("draftRelPath = %q, want %q", rel, want)
	}
	if got := draftCommitLabel(rel); got != filepath.ToSlash(filepath.Join("users", "amit", "main", hashA+".json")) {
		t.Fatalf("draftCommitLabel = %q", got)
	}
}

func TestResolveRepoPathAndRepoRootForSpace(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	spaceRoot := filepath.Join(repoRoot, "spaces", "main")
	if err := os.MkdirAll(spaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir space root: %v", err)
	}

	gotRoot, gotRel, err := resolveRepoPath(spaceRoot, "docs/page.md")
	if err != nil {
		t.Fatalf("resolveRepoPath unexpected error: %v", err)
	}
	if gotRoot != repoRoot {
		t.Fatalf("resolveRepoPath root = %q, want %q", gotRoot, repoRoot)
	}
	if gotRel != filepath.ToSlash(filepath.Join("spaces", "main", "docs", "page.md")) {
		t.Fatalf("resolveRepoPath rel = %q", gotRel)
	}

	rootOnly, err := repoRootForSpace(spaceRoot)
	if err != nil {
		t.Fatalf("repoRootForSpace unexpected error: %v", err)
	}
	if rootOnly != repoRoot {
		t.Fatalf("repoRootForSpace = %q, want %q", rootOnly, repoRoot)
	}
}
