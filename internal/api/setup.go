package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/gitops"
	wshub "mdwiki/internal/ws"
)

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)
var repoKeySanitizer = regexp.MustCompile(`[^a-z0-9._-]+`)

type setupRequest struct {
	RootLocalDir   string `json:"root_repo_local_dir"`
	StorageDir     string `json:"storage_dir"`
	FirstSpaceKey  string `json:"first_space_key"`
	FirstSpaceName string `json:"first_space_name"`
}

func absOr(value, fallback string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		v = fallback
	}
	return filepath.Abs(v)
}

func (s *Server) defaultSettings() (appsettings.Settings, error) {
	rootLocal, err := absOr(s.Cfg.RootRepoLocalDir, "/tmp/mdwiki/repos/root")
	if err != nil {
		return appsettings.Settings{}, err
	}
	storageDir, err := absOr(s.Cfg.StorageDir, "/tmp/mdwiki/state")
	if err != nil {
		return appsettings.Settings{}, err
	}
	return appsettings.Settings{
		RootRepoURL:      strings.TrimSpace(s.Cfg.RootRepoURL),
		RootRepoBranch:   strings.TrimSpace(s.Cfg.RootRepoBranch),
		RootRepoLocalDir: rootLocal,
		StorageDir:       storageDir,
		SaveMode:         "git_sync",
		Spaces:           []appsettings.SpaceEntry{},
	}, nil
}

func normalizeSaveMode(mode string) string {
	m := strings.TrimSpace(strings.ToLower(mode))
	switch m {
	case "local", "git_sync":
		return m
	default:
		return "git_sync"
	}
}

func (s *Server) rootSettingsPath(cfg appsettings.Settings) string {
	return filepath.Join(cfg.RootRepoLocalDir, s.Cfg.SpaceSettingsFile)
}

func repoBaseDir(rootRepoLocalDir string) string {
	return filepath.Dir(rootRepoLocalDir)
}

func sanitizeRepoKey(s string) string {
	out := strings.ToLower(strings.TrimSpace(s))
	out = strings.ReplaceAll(out, "/", "-")
	out = repoKeySanitizer.ReplaceAllString(out, "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-_.")
	if out == "" {
		return "repo"
	}
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-_.")
		if out == "" {
			out = "repo"
		}
	}
	return out
}

func repoDirKey(repoURL string) string {
	normalized := strings.TrimSpace(repoURL)
	if normalized == "" {
		return "repo-unknown"
	}
	if u, err := url.Parse(normalized); err == nil {
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		path := strings.Trim(strings.TrimSpace(u.EscapedPath()), "/")
		if unescaped, unescErr := url.PathUnescape(path); unescErr == nil {
			path = unescaped
		}
		path = strings.TrimSuffix(strings.ToLower(path), ".git")
		switch {
		case host != "" && path != "":
			normalized = fmt.Sprintf("%s/%s", host, path)
		case host != "":
			normalized = host
		}
	}
	base := sanitizeRepoKey(normalized)
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(normalized))))
	suffix := hex.EncodeToString(sum[:])[:8]
	return fmt.Sprintf("%s-%s", base, suffix)
}

func defaultCloneDirForRepo(rootRepoLocalDir, repoURL string) string {
	return filepath.Join(repoBaseDir(rootRepoLocalDir), repoDirKey(repoURL))
}

func (s *Server) loadSettings(ctx context.Context) (appsettings.Settings, error) {
	cfg, err := s.Store.Load(ctx)
	if err != nil {
		return appsettings.Settings{}, err
	}
	def, err := s.defaultSettings()
	if err != nil {
		return appsettings.Settings{}, err
	}
	// Root repository URL is bootstrap-only and always comes from env.
	cfg.RootRepoURL = def.RootRepoURL
	if strings.TrimSpace(cfg.RootRepoBranch) == "" {
		cfg.RootRepoBranch = def.RootRepoBranch
	}
	if strings.TrimSpace(cfg.RootRepoLocalDir) == "" {
		cfg.RootRepoLocalDir = def.RootRepoLocalDir
	}
	if strings.TrimSpace(cfg.StorageDir) == "" {
		cfg.StorageDir = def.StorageDir
	}
	cfg.SaveMode = normalizeSaveMode(cfg.SaveMode)

	p := s.rootSettingsPath(cfg)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return appsettings.Settings{}, err
	}
	if len(b) == 0 {
		return cfg, nil
	}
	var rootCfg appsettings.Settings
	if err := json.Unmarshal(b, &rootCfg); err != nil {
		return appsettings.Settings{}, err
	}
	// Local filesystem locations are node-specific and must always come from local settings/env.
	rootCfg.RootRepoLocalDir = cfg.RootRepoLocalDir
	// Never allow persisted root settings to override env root repo URL.
	rootCfg.RootRepoURL = def.RootRepoURL
	if strings.TrimSpace(rootCfg.RootRepoBranch) == "" {
		rootCfg.RootRepoBranch = cfg.RootRepoBranch
	}
	rootCfg.StorageDir = cfg.StorageDir
	rootCfg.SaveMode = normalizeSaveMode(rootCfg.SaveMode)
	return rootCfg, nil
}

func (s *Server) saveSettings(ctx context.Context, cfg appsettings.Settings) error {
	// Enforce env-configured root repo URL at persistence boundaries.
	cfg.RootRepoURL = strings.TrimSpace(s.Cfg.RootRepoURL)
	if err := s.Store.Save(ctx, cfg); err != nil {
		return err
	}
	return s.writeRootSettings(cfg)
}

func (s *Server) writeRootSettings(cfg appsettings.Settings) error {
	if err := os.MkdirAll(cfg.RootRepoLocalDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.rootSettingsPath(cfg), b, 0o644)
}

func (s *Server) syncRootSettingsToGit(r *http.Request, cfg appsettings.Settings) error {
	// Do not sync machine-local directories to git.
	repoCfg := cfg
	repoCfg.RootRepoLocalDir = ""
	repoCfg.StorageDir = ""

	b, err := json.MarshalIndent(repoCfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	if _, err := gitops.EnsureClone(cfg.RootRepoLocalDir, cfg.RootRepoURL, cfg.RootRepoBranch, s.pushAuthUsername(r), s.pushToken(r)); err != nil {
		if _, initErr := gitops.EnsureRepo(cfg.RootRepoLocalDir); initErr != nil {
			return err
		}
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

	branch := strings.TrimSpace(cfg.RootRepoBranch)
	if branch == "" {
		branch = "main"
	}
	if err := gitops.WritePageLocal(cfg.RootRepoLocalDir, branch, s.Cfg.SpaceSettingsFile, string(b), authorName, authorEmail, nil); err != nil {
		return err
	}
	return gitops.Push(cfg.RootRepoLocalDir, s.pushAuthUsername(r), s.pushToken(r), branch)
}

func (s *Server) syncRootSettingsIfEnabled(r *http.Request, cfg appsettings.Settings) error {
	if normalizeSaveMode(cfg.SaveMode) != "git_sync" {
		return nil
	}
	return s.syncRootSettingsToGit(r, cfg)
}

func (s *Server) getSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	configured := strings.TrimSpace(cfg.RootRepoURL) != "" && len(cfg.Spaces) > 0
	_ = json.NewEncoder(w).Encode(map[string]any{
		"configured": configured,
		"settings":   cfg,
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"settings": cfg,
		"storage": map[string]string{
			"implementation":   "repo_settings+local_runtime",
			"local_settings":   s.Cfg.SettingsPath,
			"root_settings":    s.rootSettingsPath(cfg),
			"storage_dir":      cfg.StorageDir,
			"root_repo_local":  cfg.RootRepoLocalDir,
			"root_repo_branch": cfg.RootRepoBranch,
		},
	})
}

type updateSettingsRequest struct {
	SaveMode string `json:"save_mode"`
}

type createSpaceRequest struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path,omitempty"`
	RepoURL     string `json:"repo_url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	LocalDir    string `json:"local_dir,omitempty"`
}

type renameSpaceRequest struct {
	DisplayName string `json:"display_name"`
}

func (s *Server) sessionLogin(r *http.Request) string {
	sid := sessionFromCookie(r)
	if sid == "" {
		return ""
	}
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		return ""
	}
	return strings.TrimSpace(sess.Login)
}

func (s *Server) requireSpaceCreator(w http.ResponseWriter, r *http.Request, sp appsettings.SpaceEntry) (string, bool) {
	login := s.sessionLogin(r)
	if login == "" {
		http.Error(w, "login required", http.StatusUnauthorized)
		return "", false
	}
	creator := strings.TrimSpace(sp.CreatedBy)
	if creator == "" {
		http.Error(w, "space has no creator metadata; cannot modify", http.StatusForbidden)
		return "", false
	}
	if !strings.EqualFold(login, creator) {
		http.Error(w, "only the space creator can perform this action", http.StatusForbidden)
		return "", false
	}
	return login, true
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg.SaveMode = normalizeSaveMode(body.SaveMode)
	if err := s.saveSettings(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.syncRootSettingsIfEnabled(r, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "settings": cfg})
}

func (s *Server) createSpace(w http.ResponseWriter, r *http.Request) {
	var body createSpaceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(strings.ToLower(body.Key))
	if key == "" || !keyPattern.MatchString(key) {
		http.Error(w, "key must match [a-z0-9][a-z0-9-_]*", http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" {
		displayName = key
	}

	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, sp := range cfg.Spaces {
		if sp.Key == key {
			http.Error(w, "space already exists", http.StatusConflict)
			return
		}
	}

	entry := appsettings.SpaceEntry{
		Key:         key,
		DisplayName: displayName,
		CreatedBy:   s.sessionLogin(r),
		RepoURL:     strings.TrimSpace(body.RepoURL),
		Branch:      strings.TrimSpace(body.Branch),
		LocalDir:    strings.TrimSpace(body.LocalDir),
	}
	if entry.RepoURL == "" {
		if _, err := gitops.EnsureClone(cfg.RootRepoLocalDir, cfg.RootRepoURL, cfg.RootRepoBranch, s.pushAuthUsername(r), s.pushToken(r)); err != nil {
			if _, initErr := gitops.EnsureRepo(cfg.RootRepoLocalDir); initErr != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		p := strings.TrimSpace(body.Path)
		if p == "" {
			p = filepath.ToSlash(filepath.Join("spaces", key))
		}
		entry.Path = p
		if entry.Branch == "" {
			entry.Branch = cfg.RootRepoBranch
		}
		if entry.Branch == "" {
			entry.Branch = "main"
		}

		spaceRoot := filepath.Join(cfg.RootRepoLocalDir, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(spaceRoot, 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := gitops.EnsureSpaceMeta(spaceRoot, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := ensureInitialized(spaceRoot, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if entry.Branch == "" {
		entry.Branch = "main"
	}

	cfg.Spaces = append(cfg.Spaces, entry)
	if err := s.saveSettings(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.syncRootSettingsIfEnabled(r, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Hub.BroadcastControlAll(wshub.Control{Type: wshub.MsgSpacesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "space": entry, "settings": cfg})
}

func (s *Server) renameSpace(w http.ResponseWriter, r *http.Request) {
	spaceKey := strings.TrimSpace(chi.URLParam(r, "space"))
	if spaceKey == "" {
		http.Error(w, "space required", http.StatusBadRequest)
		return
	}
	var body renameSpaceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nextName := strings.TrimSpace(body.DisplayName)
	if nextName == "" {
		http.Error(w, "display_name required", http.StatusBadRequest)
		return
	}

	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idx := -1
	for i := range cfg.Spaces {
		if cfg.Spaces[i].Key == spaceKey {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	if _, ok := s.requireSpaceCreator(w, r, cfg.Spaces[idx]); !ok {
		return
	}
	cfg.Spaces[idx].DisplayName = nextName

	if err := s.saveSettings(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.syncRootSettingsIfEnabled(r, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Hub.BroadcastControlAll(wshub.Control{Type: wshub.MsgSpacesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "space": cfg.Spaces[idx], "settings": cfg})
}

func (s *Server) deleteSpace(w http.ResponseWriter, r *http.Request) {
	spaceKey := strings.TrimSpace(chi.URLParam(r, "space"))
	if spaceKey == "" {
		http.Error(w, "space required", http.StatusBadRequest)
		return
	}

	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idx := -1
	for i := range cfg.Spaces {
		if cfg.Spaces[i].Key == spaceKey {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	if _, ok := s.requireSpaceCreator(w, r, cfg.Spaces[idx]); !ok {
		return
	}
	if len(cfg.Spaces) <= 1 {
		http.Error(w, "cannot delete the last space", http.StatusBadRequest)
		return
	}

	cfg.Spaces = append(cfg.Spaces[:idx], cfg.Spaces[idx+1:]...)
	if err := s.saveSettings(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.syncRootSettingsIfEnabled(r, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Hub.BroadcastControlAll(wshub.Control{Type: wshub.MsgSpacesInvalidated})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "settings": cfg})
}

func (s *Server) setupInitialSpace(w http.ResponseWriter, r *http.Request) {
	var body setupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := s.defaultSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(body.RootLocalDir) != "" {
		cfg.RootRepoLocalDir, err = filepath.Abs(body.RootLocalDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if strings.TrimSpace(body.StorageDir) != "" {
		cfg.StorageDir, err = filepath.Abs(body.StorageDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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

	if _, err := gitops.EnsureClone(cfg.RootRepoLocalDir, cfg.RootRepoURL, cfg.RootRepoBranch, "git", s.Cfg.ServerGitToken); err != nil {
		if _, initErr := gitops.EnsureRepo(cfg.RootRepoLocalDir); initErr != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	cfg.Spaces = []appsettings.SpaceEntry{{
		Key:         spaceKey,
		DisplayName: spaceName,
		CreatedBy:   s.sessionLogin(r),
		Path:        ".",
		Branch:      cfg.RootRepoBranch,
	}}

	spaceRoot := cfg.RootRepoLocalDir
	if err := os.MkdirAll(spaceRoot, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := gitops.EnsureSpaceMeta(spaceRoot, spaceKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := ensureInitialized(spaceRoot, spaceKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.saveSettings(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.syncRootSettingsIfEnabled(r, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"settings": cfg,
	})
}

func (s *Server) resolveSpaceRoot(r *http.Request, spaceKey string) (string, appsettings.SpaceEntry, bool, error) {
	cfg, err := s.loadSettings(r.Context())
	if err != nil {
		return "", appsettings.SpaceEntry{}, false, err
	}
	for _, sp := range cfg.Spaces {
		if sp.Key != spaceKey {
			continue
		}
		if strings.TrimSpace(sp.RepoURL) != "" {
			branch := strings.TrimSpace(sp.Branch)
			if branch == "" {
				branch = "main"
			}
			cloneDir := strings.TrimSpace(sp.LocalDir)
			if cloneDir == "" {
				cloneDir = defaultCloneDirForRepo(cfg.RootRepoLocalDir, sp.RepoURL)
			}
			if !filepath.IsAbs(cloneDir) {
				cloneDir = filepath.Join(repoBaseDir(cfg.RootRepoLocalDir), cloneDir)
			}
			if _, err := gitops.EnsureClone(cloneDir, sp.RepoURL, branch, s.pushAuthUsername(r), s.pushToken(r)); err != nil {
				return "", appsettings.SpaceEntry{}, false, err
			}
			if strings.TrimSpace(sp.Path) == "" || strings.TrimSpace(sp.Path) == "." {
				return cloneDir, sp, true, nil
			}
			return filepath.Join(cloneDir, filepath.FromSlash(sp.Path)), sp, true, nil
		}

		if _, err := gitops.EnsureClone(cfg.RootRepoLocalDir, cfg.RootRepoURL, cfg.RootRepoBranch, s.pushAuthUsername(r), s.pushToken(r)); err != nil {
			if _, initErr := gitops.EnsureRepo(cfg.RootRepoLocalDir); initErr != nil {
				return "", appsettings.SpaceEntry{}, false, err
			}
		}
		if strings.TrimSpace(sp.Path) == "" || strings.TrimSpace(sp.Path) == "." {
			return cfg.RootRepoLocalDir, sp, true, nil
		}
		return filepath.Join(cfg.RootRepoLocalDir, filepath.FromSlash(sp.Path)), sp, true, nil
	}
	return "", appsettings.SpaceEntry{}, false, nil
}
