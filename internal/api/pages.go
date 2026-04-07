package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/session"
	wshub "mdwiki/internal/ws"
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

type renamePageBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func normalizeMarkdownRelPath(raw string) (string, error) {
	relPath := strings.TrimSpace(filepath.ToSlash(raw))
	if relPath == "" {
		return "", errors.New("path is required")
	}
	if !strings.HasSuffix(strings.ToLower(relPath), ".md") {
		relPath += ".md"
	}
	if strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "../") {
		return "", errors.New("invalid path")
	}
	return relPath, nil
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
	relPath, err := normalizeMarkdownRelPath(body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	s.Hub.BroadcastControlToSpace(spaceKey, wshub.Control{Type: wshub.MsgPagesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath, "content": seed})
}

func (s *Server) renamePage(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	var body renamePageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fromPath, err := normalizeMarkdownRelPath(body.From)
	if err != nil {
		http.Error(w, "invalid source path", http.StatusBadRequest)
		return
	}
	toPath, err := normalizeMarkdownRelPath(body.To)
	if err != nil {
		http.Error(w, "invalid destination path", http.StatusBadRequest)
		return
	}
	if fromPath == toPath {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": toPath})
		return
	}
	fromFull := filepath.Join(root, filepath.FromSlash(fromPath))
	toFull := filepath.Join(root, filepath.FromSlash(toPath))
	if _, err := os.Stat(fromFull); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "source page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := os.Stat(toFull); err == nil {
		http.Error(w, "destination page already exists", http.StatusConflict)
		return
	}

	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saveMode := normalizeSaveMode(cfg.SaveMode)
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Name) != "" {
				authorName = sess.Name
			} else if strings.TrimSpace(sess.Login) != "" {
				authorName = sess.Login
			}
			if strings.TrimSpace(sess.Login) != "" {
				authorEmail = sess.Login + "@users.noreply.github.com"
			}
		}
	}
	if saveMode == "local" {
		if err := os.MkdirAll(filepath.Dir(toFull), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(fromFull, toFull); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		repoRootFrom, repoRelFrom, err := resolveRepoPath(root, fromPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		repoRootTo, repoRelTo, err := resolveRepoPath(root, toPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if repoRootFrom != repoRootTo {
			http.Error(w, "cannot rename across repositories", http.StatusBadRequest)
			return
		}
		_, err = s.executeGitWrite(r.Context(), gitWriteJob{
			ID:          session.NewID(),
			Op:          "rename",
			RepoRoot:    repoRootFrom,
			Branch:      branch,
			FromPath:    repoRelFrom,
			ToPath:      repoRelTo,
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			PushToken:   s.pushToken(r),
			StrictPush:  true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.Hub.BroadcastControlToSpace(spaceKey, wshub.Control{Type: wshub.MsgPagesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": toPath})
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}

	relPath, err := normalizeMarkdownRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if _, err := os.Stat(full); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saveMode := normalizeSaveMode(cfg.SaveMode)
	branch := strings.TrimSpace(ent.Branch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Name) != "" {
				authorName = sess.Name
			} else if strings.TrimSpace(sess.Login) != "" {
				authorName = sess.Login
			}
			if strings.TrimSpace(sess.Login) != "" {
				authorEmail = sess.Login + "@users.noreply.github.com"
			}
		}
	}

	if saveMode == "local" {
		if err := os.Remove(full); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "page not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		repoRoot, repoRelPath, err := resolveRepoPath(root, relPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = s.executeGitWrite(r.Context(), gitWriteJob{
			ID:          session.NewID(),
			Op:          "delete",
			RepoRoot:    repoRoot,
			Branch:      branch,
			Path:        repoRelPath,
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			PushToken:   s.pushToken(r),
			StrictPush:  true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.Hub.BroadcastControlToSpace(spaceKey, wshub.Control{Type: wshub.MsgPagesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath})
}

func buildPageTree(root string) ([]pageTreeNode, error) {
	type folder struct {
		name    string
		path    string
		folders map[string]*folder
		order   []string
		pages   []pageTreeNode
	}
	mkFolder := func(name, path string) *folder {
		return &folder{name: name, path: path, folders: map[string]*folder{}, order: []string{}, pages: []pageTreeNode{}}
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
				current.order = append(current.order, part)
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
		for _, name := range f.order {
			child := f.folders[name]
			out = append(out, pageTreeNode{
				Name:     child.name,
				Path:     child.path,
				Type:     "folder",
				Children: fold(child),
			})
		}
		out = append(out, f.pages...)
		return out
	}

	return fold(rootFolder), nil
}
