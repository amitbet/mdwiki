package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/gitops"
)

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)

type setupRequest struct {
	RootRepoPath   string `json:"root_repo_path"`
	FirstSpaceKey  string `json:"first_space_key"`
	FirstSpaceName string `json:"first_space_name"`
}

func (s *Server) getSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	configured := cfg.RootRepoPath != "" && len(cfg.Spaces) > 0
	_ = json.NewEncoder(w).Encode(map[string]any{
		"configured": configured,
		"settings":   cfg,
	})
}

func (s *Server) setupInitialSpace(w http.ResponseWriter, r *http.Request) {
	var body setupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.RootRepoPath) == "" {
		http.Error(w, "root_repo_path is required", http.StatusBadRequest)
		return
	}
	rootRepo, err := filepath.Abs(body.RootRepoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	spaceKey := strings.TrimSpace(strings.ToLower(body.FirstSpaceKey))
	if spaceKey == "" {
		spaceKey = "main"
	}
	if !keyPattern.MatchString(spaceKey) {
		http.Error(w, "first_space_key must match [a-z0-9][a-z0-9-_]*", http.StatusBadRequest)
		return
	}
	spaceName := strings.TrimSpace(body.FirstSpaceName)
	if spaceName == "" {
		spaceName = "Main Space"
	}

	if _, err := gitops.EnsureRepo(rootRepo); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spacePath := filepath.ToSlash(filepath.Join("spaces", spaceKey))
	spaceRoot := filepath.Join(rootRepo, filepath.FromSlash(spacePath))
	if err := os.MkdirAll(spaceRoot, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := gitops.EnsureSpaceMeta(spaceRoot, spaceKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	readme := filepath.Join(spaceRoot, "README.md")
	if _, err := os.Stat(readme); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(readme, []byte("# Welcome\n\nStart writing in your new wiki space.\n"), 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	cfg := appsettings.Settings{
		RootRepoPath: rootRepo,
		Spaces: []appsettings.SpaceEntry{{
			Key:         spaceKey,
			DisplayName: spaceName,
			Path:        spacePath,
		}},
	}
	if err := s.Store.Save(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeRootSettings(rootRepo, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"settings": cfg,
	})
}

func writeRootSettings(root string, cfg appsettings.Settings) error {
	metaDir := filepath.Join(root, ".mdwiki")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(metaDir, "settings.json"), b, 0o644)
}

func (s *Server) resolveSpaceRoot(r *http.Request, spaceKey string) (string, appsettings.SpaceEntry, bool, error) {
	cfg, err := s.Store.Load(r.Context())
	if err != nil {
		return "", appsettings.SpaceEntry{}, false, err
	}
	for _, sp := range cfg.Spaces {
		if sp.Key == spaceKey {
			if filepath.IsAbs(sp.Path) {
				return sp.Path, sp, true, nil
			}
			return filepath.Join(cfg.RootRepoPath, filepath.FromSlash(sp.Path)), sp, true, nil
		}
	}
	return "", appsettings.SpaceEntry{}, false, nil
}
