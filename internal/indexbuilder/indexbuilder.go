package indexbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// PageRow matches index.schema.json pages[] entry.
type PageRow struct {
	PageID        string  `json:"page_id"`
	Path          string  `json:"path"`
	Title         string  `json:"title"`
	ParentPageID  *string `json:"parent_page_id"`
	Slug          string  `json:"slug,omitempty"`
}

// IndexDoc is `.mdwiki/index.json` shape.
type IndexDoc struct {
	SchemaVersion int        `json:"schema_version"`
	SpaceID       string     `json:"space_id"`
	UpdatedAt     string     `json:"updated_at,omitempty"`
	Pages         []PageRow  `json:"pages"`
}

// ScanMarkdown scans space root for *.md and writes index.json.
func ScanMarkdown(spaceRoot, spaceID string) (*IndexDoc, error) {
	var pages []PageRow
	err := filepath.Walk(spaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(path, filepath.Join(spaceRoot, ".mdwiki")) {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(spaceRoot, path)
		rel = filepath.ToSlash(rel)
		title := strings.TrimSuffix(filepath.Base(rel), ".md")
		pages = append(pages, PageRow{
			PageID: uuid.NewString(),
			Path:   rel,
			Title:  title,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &IndexDoc{
		SchemaVersion: 1,
		SpaceID:       spaceID,
		Pages:         pages,
	}, nil
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
