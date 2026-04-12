package indexbuilder

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// PageRow matches index.schema.json pages[] entry.
type PageRow struct {
	PageID       string  `json:"page_id"`
	Path         string  `json:"path"`
	Title        string  `json:"title"`
	ParentPageID *string `json:"parent_page_id"`
	Slug         string  `json:"slug,omitempty"`
}

// IndexDoc is `.mdwiki/index.json` shape.
type IndexDoc struct {
	SchemaVersion int       `json:"schema_version"`
	SpaceID       string    `json:"space_id"`
	UpdatedAt     string    `json:"updated_at,omitempty"`
	Pages         []PageRow `json:"pages"`
}

// ScanMarkdown scans space root for *.md and writes index.json.
func ScanMarkdown(spaceRoot, spaceID string) (*IndexDoc, error) {
	existing, _ := LoadIndex(spaceRoot)
	byPath := map[string]PageRow{}
	if existing != nil {
		for _, page := range existing.Pages {
			byPath[page.Path] = page
		}
	}

	var pages []PageRow
	err := filepath.Walk(spaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldSkipDir(spaceRoot, path, info) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(spaceRoot, path)
		rel = filepath.ToSlash(rel)
		title := strings.TrimSuffix(filepath.Base(rel), ".md")
		page := byPath[rel]
		if strings.TrimSpace(page.PageID) == "" {
			page.PageID = uuid.NewString()
		}
		page.Path = rel
		page.Title = title
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Path < pages[j].Path
	})
	return &IndexDoc{
		SchemaVersion: 1,
		SpaceID:       spaceID,
		Pages:         pages,
	}, nil
}

func LoadIndex(spaceRoot string) (*IndexDoc, error) {
	raw, err := os.ReadFile(filepath.Join(spaceRoot, ".mdwiki", "index.json"))
	if err != nil {
		return nil, err
	}
	var doc IndexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// WriteIndex writes `.mdwiki/index.json`.
func WriteIndex(spaceRoot string, doc *IndexDoc) error {
	dir := filepath.Join(spaceRoot, ".mdwiki")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644)
}

func RenamePage(spaceRoot, oldPath, newPath string) (*IndexDoc, error) {
	doc, err := LoadIndex(spaceRoot)
	if err != nil {
		return nil, err
	}
	for i := range doc.Pages {
		if doc.Pages[i].Path == oldPath {
			doc.Pages[i].Path = newPath
			doc.Pages[i].Title = strings.TrimSuffix(filepath.Base(newPath), filepath.Ext(newPath))
			return doc, WriteIndex(spaceRoot, doc)
		}
	}
	return nil, errors.New("page not found in index")
}

func shouldSkipDir(spaceRoot, path string, info os.FileInfo) bool {
	if path == spaceRoot {
		return false
	}
	name := info.Name()
	if strings.HasPrefix(name, ".") {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	return false
}
