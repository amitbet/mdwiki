package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

type pageTreeNode struct {
	Name     string         `json:"name"`
	Path     string         `json:"path"`
	Type     string         `json:"type"`
	Children []pageTreeNode `json:"children,omitempty"`
}

type createPageBody struct {
	Path string `json:"path"`
}

func (s *Server) listPages(w http.ResponseWriter, r *http.Request) {
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
	if err := os.MkdirAll(root, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tree, err := buildPageTree(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree})
}

func (s *Server) createPage(w http.ResponseWriter, r *http.Request) {
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
	var body createPageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	relPath := strings.TrimSpace(filepath.ToSlash(body.Path))
	if relPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	if !strings.HasSuffix(strings.ToLower(relPath), ".md") {
		relPath += ".md"
	}
	if strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "../") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := os.Stat(full); err == nil {
		http.Error(w, "page already exists", http.StatusConflict)
		return
	}
	seed := "# New Page\n\nStart writing here.\n"
	if err := os.WriteFile(full, []byte(seed), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath, "content": seed})
}

func buildPageTree(root string) ([]pageTreeNode, error) {
	type folder struct {
		name    string
		path    string
		folders map[string]*folder
		pages   []pageTreeNode
	}
	mkFolder := func(name, path string) *folder {
		return &folder{name: name, path: path, folders: map[string]*folder{}, pages: []pageTreeNode{}}
	}

	rootFolder := mkFolder("", "")

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == ".mdwiki" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = ""
		}
		parts := []string{}
		if dir != "" {
			parts = strings.Split(dir, "/")
		}
		current := rootFolder
		currentPath := ""
		for _, part := range parts {
			if currentPath == "" {
				currentPath = part
			} else {
				currentPath += "/" + part
			}
			next, ok := current.folders[part]
			if !ok {
				next = mkFolder(part, currentPath)
				current.folders[part] = next
			}
			current = next
		}
		base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		current.pages = append(current.pages, pageTreeNode{
			Name: base,
			Path: rel,
			Type: "page",
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	var fold func(*folder) []pageTreeNode
	fold = func(f *folder) []pageTreeNode {
		out := make([]pageTreeNode, 0, len(f.folders)+len(f.pages))
		folderNames := make([]string, 0, len(f.folders))
		for name := range f.folders {
			folderNames = append(folderNames, name)
		}
		sort.Strings(folderNames)
		for _, name := range folderNames {
			child := f.folders[name]
			out = append(out, pageTreeNode{
				Name:     child.name,
				Path:     child.path,
				Type:     "folder",
				Children: fold(child),
			})
		}
		sort.Slice(f.pages, func(i, j int) bool {
			return strings.ToLower(f.pages[i].Path) < strings.ToLower(f.pages[j].Path)
		})
		out = append(out, f.pages...)
		return out
	}

	return fold(rootFolder), nil
}
