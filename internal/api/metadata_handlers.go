package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/indexbuilder"
	"mdwiki/internal/metadata"
)

type applyTemplateBody struct {
	Path            string `json:"path"`
	Template        string `json:"template"`
	IncludeSections bool   `json:"include_sections"`
}

func (s *Server) initializeSpace(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	doc, err := indexbuilder.ScanMarkdown(root, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	indexRaw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	spaceJSON := `{"schema_version":1,"space_id":"` + spaceKey + `","display_name":"` + spaceKey + `","default_branch":"main"}` + "\n"
	if err := s.writeManagedFile(r.Context(), r, root, "", ".mdwiki/space.json", spaceJSON, "wiki: initialize space metadata"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.writeManagedFile(r.Context(), r, root, "", ".mdwiki/index.json", string(indexRaw)+"\n", "wiki: initialize routing index"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "initialized": true, "index": doc})
}

func (s *Server) repoFile(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	relPath, err := normalizeRepoRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (strings.HasPrefix(relPath, ".mdwiki/") && !strings.HasPrefix(relPath, ".mdwiki/assets/")) || strings.HasPrefix(filepath.Base(relPath), ".") {
		http.Error(w, "forbidden path", http.StatusForbidden)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}
	if ctype := mime.TypeByExtension(filepath.Ext(relPath)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(w, r, full)
}

func (s *Server) applyTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.UseMetadata {
		http.Error(w, "metadata support is disabled", http.StatusConflict)
		return
	}
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	if !spaceInitialized(root) {
		http.Error(w, "space not initialized; initialize mdwiki for this repo first", http.StatusConflict)
		return
	}
	var body applyTemplateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pagePath, err := normalizeMarkdownRelPath(body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	templateName := strings.TrimSpace(strings.ToLower(body.Template))
	spec, ok := templateCatalog()[templateName]
	if !ok {
		http.Error(w, "unknown template", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pagePath))); err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	if _, err := ensureInitialized(root, spaceKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	existing, err := metadata.Read(root, pagePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	doc := metadata.Doc{}
	if existing != nil {
		doc.Title = existing.Title
		doc.Summary = existing.Summary
		doc.Status = existing.Status
		doc.Owners = existing.Owners
		doc.Reviewers = existing.Reviewers
		doc.Tags = existing.Tags
		doc.CreatedAt = existing.CreatedAt
	}
	doc.DocType = spec.DocType
	doc.Template = templateName
	doc.Links = spec.Links
	if templateName == "blank" {
		doc.DocType = ""
		doc.Template = ""
		doc.Links = nil
	}
	sidecarPath := metadata.SidecarRelPath(pagePath)
	if isMetadataDocEmpty(doc) {
		_ = metadata.Delete(root, pagePath)
		if err := s.deleteManagedFile(r.Context(), r, root, "", sidecarPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		if err := metadata.Write(root, pagePath, doc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		metaRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sidecarPath)))
		if err == nil {
			if err := s.writeManagedFile(r.Context(), r, root, "", sidecarPath, string(metaRaw), "wiki: update metadata for "+pagePath); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	if body.IncludeSections && templateName != "blank" {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pagePath)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		next := strings.TrimRight(string(raw), "\n")
		if strings.TrimSpace(next) != "" {
			next += "\n\n"
		}
		next += spec.SectionMarkdown
		if err := s.writeManagedFile(r.Context(), r, root, "", pagePath, next+"\n", "wiki: apply template "+templateName+" to "+pagePath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": pagePath, "metadata_path": sidecarPath, "template": templateName})
}

func (s *Server) incomingLinks(root, pagePath string) ([]metadata.Link, error) {
	if !s.Cfg.UseMetadata {
		return nil, nil
	}
	pageID := pageIDForPath(root, pagePath)
	index, err := indexbuilder.LoadIndex(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var links []metadata.Link
	for _, row := range index.Pages {
		doc, err := metadata.Read(root, row.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, link := range doc.Links {
			if strings.TrimSpace(link.TargetPageID) == pageID || strings.TrimSpace(link.TargetPath) == pagePath {
				links = append(links, link)
			}
		}
	}
	return links, nil
}

type templateSpec struct {
	DocType         string
	Links           []metadata.Link
	SectionMarkdown string
}

func isMetadataDocEmpty(doc metadata.Doc) bool {
	return strings.TrimSpace(doc.Title) == "" &&
		strings.TrimSpace(doc.Summary) == "" &&
		strings.TrimSpace(doc.DocType) == "" &&
		strings.TrimSpace(doc.Status) == "" &&
		len(doc.Owners) == 0 &&
		len(doc.Reviewers) == 0 &&
		len(doc.Tags) == 0 &&
		len(doc.Links) == 0 &&
		strings.TrimSpace(doc.Template) == ""
}

func templateCatalog() map[string]templateSpec {
	return map[string]templateSpec{
		"blank": {
			DocType:         "",
			Links:           nil,
			SectionMarkdown: "",
		},
		"prd": {
			DocType: "prd",
			Links: []metadata.Link{
				{Rel: "implemented_by", Title: "Spec"},
				{Rel: "implemented_by", Title: "Detailed Design"},
				{Rel: "implemented_by", Title: "Plan"},
				{Rel: "related_to", Title: "ADR"},
			},
			SectionMarkdown: "## Overview\n\n## Goals\n\n## Non-Goals\n\n## Requirements\n\n## Acceptance Criteria\n\n## Related Documents",
		},
		"spec": {
			DocType: "spec",
			Links: []metadata.Link{
				{Rel: "implements", Title: "PRD"},
				{Rel: "realized_by", Title: "Detailed Design"},
				{Rel: "related_to", Title: "ADR"},
				{Rel: "references", Title: "OpenAPI / JSON Schema"},
			},
			SectionMarkdown: "## Overview\n\n## Functional Behavior\n\n## Interfaces\n\n## Edge Cases\n\n## Constraints\n\n## Related Documents",
		},
		"detailed_design": {
			DocType: "detailed_design",
			Links: []metadata.Link{
				{Rel: "implements", Title: "PRD"},
				{Rel: "implements", Title: "Spec"},
				{Rel: "constrained_by", Title: "ADR"},
				{Rel: "executed_by", Title: "Plan"},
			},
			SectionMarkdown: "## Overview\n\n## Architecture\n\n## Data Model\n\n## Interfaces\n\n## Rollout\n\n## Tradeoffs\n\n## Related Documents",
		},
		"plan": {
			DocType: "plan",
			Links: []metadata.Link{
				{Rel: "for", Title: "PRD"},
				{Rel: "for", Title: "Spec"},
				{Rel: "for", Title: "Detailed Design"},
				{Rel: "blocked_by", Title: "Related Doc"},
			},
			SectionMarkdown: "## Overview\n\n## Milestones\n\n## Tasks\n\n## Risks\n\n## Related Documents",
		},
		"adr": {
			DocType: "adr",
			Links: []metadata.Link{
				{Rel: "affects", Title: "Spec"},
				{Rel: "affects", Title: "Detailed Design"},
				{Rel: "affects", Title: "Plan"},
				{Rel: "supersedes", Title: "ADR"},
			},
			SectionMarkdown: "## Status\n\n## Context\n\n## Decision\n\n## Consequences\n\n## Alternatives\n\n## Related Documents",
		},
	}
}
