package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/gitops"
	"mdwiki/internal/session"
)

type createDiagramBody struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type updateDiagramBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func normalizeAssetRelPath(raw string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(raw))
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, "../") {
		return "", fmt.Errorf("invalid path")
	}
	if !strings.HasPrefix(rel, "assets/") && !strings.HasPrefix(rel, ".mdwiki/assets/") {
		return "", fmt.Errorf("asset path must be under assets/")
	}
	return rel, nil
}

func sanitizeAssetName(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "..", "-")
	if name == "" {
		return "asset"
	}
	return name
}

func diagramTemplate(kind string) string {
	switch kind {
	case "excalidraw":
		return "{\n  \"type\": \"excalidraw\",\n  \"version\": 2,\n  \"source\": \"mdwiki\",\n  \"elements\": [],\n  \"appState\": {\n    \"viewBackgroundColor\": \"#ffffff\",\n    \"gridSize\": null\n  },\n  \"files\": {}\n}\n"
	default:
		return "<mxfile host=\"app.diagrams.net\" version=\"24.7.17\"><diagram id=\"mdwiki\" name=\"Page-1\"></diagram></mxfile>\n"
	}
}

func (s *Server) assetFile(w http.ResponseWriter, r *http.Request) {
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
	relPath, err := normalizeAssetRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b, err := gitops.ReadFile(root, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if ctype := mime.TypeByExtension(filepath.Ext(relPath)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func (s *Server) uploadImageAsset(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, readErr := io.ReadAll(file)
	if readErr != nil {
		http.Error(w, readErr.Error(), http.StatusBadRequest)
		return
	}
	name := sanitizeAssetName(header.Filename)
	if filepath.Ext(name) == "" {
		name += ".bin"
	}
	relPath := filepath.ToSlash(filepath.Join("assets", "images", fmt.Sprintf("%d-%s", time.Now().UnixNano(), name)))
	if err := s.writeAssetFile(r, root, ent.Branch, relPath, data, fmt.Sprintf("asset: upload %s", relPath)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath, "url": assetURL(spaceKey, relPath)})
}

func (s *Server) createDiagram(w http.ResponseWriter, r *http.Request) {
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
	var body createDiagramBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	if kind != "excalidraw" && kind != "drawio" {
		http.Error(w, "kind must be excalidraw or drawio", http.StatusBadRequest)
		return
	}
	name := sanitizeAssetName(body.Name)
	if name == "asset" {
		name = "diagram"
	}
	ext := ".drawio"
	if kind == "excalidraw" {
		ext = ".excalidraw"
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	relPath := filepath.ToSlash(filepath.Join("assets", "diagrams", name))
	content := diagramTemplate(kind)
	if err := s.writeAssetFile(r, root, ent.Branch, relPath, []byte(content), fmt.Sprintf("diagram: create %s", relPath)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath, "kind": kind, "content": content, "url": assetURL(spaceKey, relPath)})
}

func (s *Server) getDiagram(w http.ResponseWriter, r *http.Request) {
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
	relPath, err := normalizeAssetRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b, err := gitops.ReadFile(root, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath, "content": string(b)})
}

func (s *Server) updateDiagram(w http.ResponseWriter, r *http.Request) {
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
	var body updateDiagramBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	relPath, err := normalizeAssetRelPath(body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasSuffix(strings.ToLower(relPath), ".drawio") && !strings.HasSuffix(strings.ToLower(relPath), ".excalidraw") {
		http.Error(w, "path must be a diagram file", http.StatusBadRequest)
		return
	}
	if err := s.writeAssetFile(r, root, ent.Branch, relPath, []byte(body.Content), fmt.Sprintf("diagram: update %s", relPath)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": relPath})
}

func (s *Server) writeAssetFile(r *http.Request, spaceRoot, branch, relPath string, data []byte, commitMsg string) error {
	repoRoot, repoRelPath, err := resolveRepoPath(spaceRoot, relPath)
	if err != nil {
		return err
	}
	resolvedBranch := strings.TrimSpace(branch)
	if resolvedBranch == "" {
		cfg, err := s.loadSettings(r.Context())
		if err != nil {
			return err
		}
		resolvedBranch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if resolvedBranch == "" {
		resolvedBranch = "main"
	}
	authorName, authorEmail := authorFromRequest(s, r)
	_, err = s.executeGitWrite(r.Context(), gitWriteJob{
		ID:          session.NewID(),
		Op:          "save",
		RepoRoot:    repoRoot,
		Branch:      resolvedBranch,
		Path:        repoRelPath,
		ContentB64:  gitops.EncodeBytesBase64(data),
		CommitMsg:   commitMsg,
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		PushUser:    s.pushAuthUsername(r),
		PushToken:   s.pushToken(r),
	})
	return err
}

func assetURL(spaceKey, relPath string) string {
	return "/api/spaces/" + url.PathEscape(spaceKey) + "/asset?path=" + url.QueryEscape(relPath)
}
