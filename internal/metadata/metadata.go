package metadata

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Link struct {
	Rel          string `yaml:"rel" json:"rel"`
	TargetPageID string `yaml:"target_page_id,omitempty" json:"target_page_id,omitempty"`
	TargetPath   string `yaml:"target_path,omitempty" json:"target_path,omitempty"`
	Title        string `yaml:"title,omitempty" json:"title,omitempty"`
}

type Doc struct {
	Title     string   `yaml:"title,omitempty" json:"title,omitempty"`
	Summary   string   `yaml:"summary,omitempty" json:"summary,omitempty"`
	DocType   string   `yaml:"doc_type,omitempty" json:"doc_type,omitempty"`
	Status    string   `yaml:"status,omitempty" json:"status,omitempty"`
	Owners    []string `yaml:"owners,omitempty" json:"owners,omitempty"`
	Reviewers []string `yaml:"reviewers,omitempty" json:"reviewers,omitempty"`
	Tags      []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Links     []Link   `yaml:"links,omitempty" json:"links,omitempty"`
	Template  string   `yaml:"template,omitempty" json:"template,omitempty"`
	CreatedAt string   `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt string   `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

func SidecarRelPath(pagePath string) string {
	trimmed := strings.TrimSuffix(filepath.ToSlash(pagePath), filepath.Ext(pagePath))
	return filepath.ToSlash(filepath.Join(".mdwiki", "pages", filepath.FromSlash(trimmed)+".meta.yaml"))
}

func Read(spaceRoot, pagePath string) (*Doc, error) {
	raw, err := os.ReadFile(filepath.Join(spaceRoot, filepath.FromSlash(SidecarRelPath(pagePath))))
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var doc Doc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func Write(spaceRoot, pagePath string, doc Doc) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(doc.CreatedAt) == "" {
		doc.CreatedAt = now
	}
	doc.UpdatedAt = now
	relPath := SidecarRelPath(pagePath)
	full := filepath.Join(spaceRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(full, out, 0o644)
}

func Delete(spaceRoot, pagePath string) error {
	full := filepath.Join(spaceRoot, filepath.FromSlash(SidecarRelPath(pagePath)))
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func Rename(spaceRoot, oldPagePath, newPagePath string) error {
	oldFull := filepath.Join(spaceRoot, filepath.FromSlash(SidecarRelPath(oldPagePath)))
	newFull := filepath.Join(spaceRoot, filepath.FromSlash(SidecarRelPath(newPagePath)))
	if _, err := os.Stat(oldFull); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
		return err
	}
	return os.Rename(oldFull, newFull)
}
