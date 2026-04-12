package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/config"
	"mdwiki/internal/gitops"
	"mdwiki/internal/session"
	"mdwiki/internal/space"
	wshub "mdwiki/internal/ws"
)

type fakeSettingsStore struct {
	load    appsettings.Settings
	loadErr error
	saveErr error
	saved   []appsettings.Settings
}

func (f *fakeSettingsStore) Load(context.Context) (appsettings.Settings, error) {
	return f.load, f.loadErr
}

func (f *fakeSettingsStore) Save(_ context.Context, cfg appsettings.Settings) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, cfg)
	f.load = cfg
	return nil
}

func newTestServer(t *testing.T, store appsettings.Store) *Server {
	t.Helper()
	return New(config.Config{
		RootRepoURL:       "https://github.com/amitbet/documents",
		RootRepoBranch:    "main",
		RootRepoLocalDir:  filepath.Join(t.TempDir(), "repos", "root"),
		StorageDir:        filepath.Join(t.TempDir(), "state"),
		SettingsPath:      filepath.Join(t.TempDir(), "settings.json"),
		SpaceSettingsFile: "mdwiki.spaces.json",
		FrontendOrigin:    "http://localhost:5173",
		ServerGitToken:    "server-token",
	}, &space.Registry{}, store, session.NewStore(), wshub.NewHub(nil), nil)
}

func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestSetupHelpers(t *testing.T) {
	got, err := absOr("", "tmp/example")
	if err != nil {
		t.Fatalf("absOr fallback error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("absOr fallback should return abs path, got %q", got)
	}

	value, err := absOr("relative/path", "ignored")
	if err != nil {
		t.Fatalf("absOr value error: %v", err)
	}
	if !filepath.IsAbs(value) {
		t.Fatalf("absOr value should return abs path, got %q", value)
	}

	if normalizeSaveMode(" LOCAL ") != "local" {
		t.Fatalf("normalizeSaveMode(local) mismatch")
	}
	if normalizeSaveMode("weird") != "git_sync" {
		t.Fatalf("normalizeSaveMode should default to git_sync")
	}

	root := filepath.Join("/tmp", "mdwiki", "repos", "root")
	if repoBaseDir(root) != filepath.Join("/tmp", "mdwiki", "repos") {
		t.Fatalf("repoBaseDir mismatch")
	}
	if sanitizeRepoKey(" GitHub.com/AmitBet/Documents.git ") != "github.com-amitbet-documents.git" {
		t.Fatalf("sanitizeRepoKey mismatch")
	}

	key := repoDirKey("https://github.com/AmitBet/Documents.git")
	if !strings.HasPrefix(key, "github.com-amitbet-documents-") {
		t.Fatalf("repoDirKey prefix mismatch: %q", key)
	}
	if len(key) <= len("github.com-amitbet-documents-") {
		t.Fatalf("repoDirKey should include hash suffix: %q", key)
	}

	cloneDir := defaultCloneDirForRepo(root, "https://github.com/AmitBet/Documents.git")
	if filepath.Dir(cloneDir) != repoBaseDir(root) {
		t.Fatalf("defaultCloneDirForRepo parent mismatch: %q", cloneDir)
	}
	if filepath.Base(cloneDir) != key {
		t.Fatalf("defaultCloneDirForRepo base = %q, want %q", filepath.Base(cloneDir), key)
	}
}

func TestDefaultSettingsAndLoadSettingsMerge(t *testing.T) {
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoBranch:   "",
			RootRepoLocalDir: "",
			StorageDir:       "",
			SaveMode:         "weird",
		},
	}
	srv := newTestServer(t, store)

	def, err := srv.defaultSettings()
	if err != nil {
		t.Fatalf("defaultSettings error: %v", err)
	}
	if def.RootRepoURL != srv.Cfg.RootRepoURL || def.RootRepoBranch != "main" {
		t.Fatalf("defaultSettings mismatch: %+v", def)
	}
	if !filepath.IsAbs(def.RootRepoLocalDir) || !filepath.IsAbs(def.StorageDir) {
		t.Fatalf("defaultSettings should use abs dirs: %+v", def)
	}
	if def.SaveMode != "git_sync" {
		t.Fatalf("defaultSettings SaveMode = %q", def.SaveMode)
	}

	cfg, err := srv.loadSettings(context.Background())
	if err != nil {
		t.Fatalf("loadSettings error: %v", err)
	}
	if cfg.RootRepoURL != srv.Cfg.RootRepoURL {
		t.Fatalf("loadSettings should enforce env root repo url, got %q", cfg.RootRepoURL)
	}
	if cfg.RootRepoBranch != "main" || cfg.SaveMode != "git_sync" {
		t.Fatalf("loadSettings normalization mismatch: %+v", cfg)
	}
	if cfg.RootRepoLocalDir == "" || cfg.StorageDir == "" {
		t.Fatalf("loadSettings should fill default dirs: %+v", cfg)
	}
}

func TestLoadSettingsMergesRepoSettingsButKeepsLocalRuntime(t *testing.T) {
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoBranch:   "main",
			RootRepoLocalDir: filepath.Join(t.TempDir(), "runtime-root"),
			StorageDir:       filepath.Join(t.TempDir(), "runtime-state"),
			SaveMode:         "local",
		},
	}
	srv := newTestServer(t, store)

	rootSettingsPath := srv.rootSettingsPath(store.load)
	if err := os.MkdirAll(filepath.Dir(rootSettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir root settings dir: %v", err)
	}
	repoCfg := appsettings.Settings{
		RootRepoURL:      "https://example.com/ignored.git",
		RootRepoBranch:   "develop",
		RootRepoLocalDir: "/bad/override",
		StorageDir:       "/bad/storage",
		SaveMode:         "git_sync",
		Spaces:           []appsettings.SpaceEntry{{Key: "main", DisplayName: "Main"}},
	}
	b, _ := json.Marshal(repoCfg)
	if err := os.WriteFile(rootSettingsPath, b, 0o644); err != nil {
		t.Fatalf("write root settings: %v", err)
	}

	cfg, err := srv.loadSettings(context.Background())
	if err != nil {
		t.Fatalf("loadSettings error: %v", err)
	}
	if cfg.RootRepoURL != srv.Cfg.RootRepoURL {
		t.Fatalf("repo settings should not override env url, got %q", cfg.RootRepoURL)
	}
	if cfg.RootRepoLocalDir != store.load.RootRepoLocalDir || cfg.StorageDir != store.load.StorageDir {
		t.Fatalf("repo settings should not override local runtime paths: %+v", cfg)
	}
	if cfg.RootRepoBranch != "develop" || cfg.SaveMode != "git_sync" || len(cfg.Spaces) != 1 {
		t.Fatalf("repo settings should contribute branch/save_mode/spaces: %+v", cfg)
	}
}

func TestListSpacesUsesRootRepoFallbacks(t *testing.T) {
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: filepath.Join(t.TempDir(), "root"),
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			Spaces: []appsettings.SpaceEntry{
				{Key: "main", DisplayName: "Main", CreatedBy: "amit"},
				{Key: "other", DisplayName: "Other", RepoURL: "https://github.com/acme/other.git", Branch: "develop"},
			},
		},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/api/spaces", nil)
	rr := httptest.NewRecorder()
	srv.listSpaces(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("listSpaces status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode listSpaces: %v", err)
	}
	if got[0]["repo_url"] != store.load.RootRepoURL || got[0]["branch"] != "main" {
		t.Fatalf("main space should fall back to root repo/branch: %#v", got[0])
	}
	if got[1]["repo_url"] != "https://github.com/acme/other.git" || got[1]["branch"] != "develop" {
		t.Fatalf("other space should keep explicit repo/branch: %#v", got[1])
	}
}

func TestSessionAndPushHelpers(t *testing.T) {
	store := &fakeSettingsStore{}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if sessionFromCookie(req) != "" {
		t.Fatalf("sessionFromCookie should be empty without cookie")
	}
	if srv.pushAuthUsername(req) != "git" {
		t.Fatalf("pushAuthUsername should fall back to git")
	}
	if srv.pushToken(req) != srv.Cfg.ServerGitToken {
		t.Fatalf("pushToken should fall back to server token")
	}

	sess := &session.Session{ID: "sid-1", Login: "amitbet", Name: "Amit", AccessToken: "user-token"}
	srv.Sessions.Put(sess)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})

	if sessionFromCookie(req) != sess.ID {
		t.Fatalf("sessionFromCookie mismatch")
	}
	if srv.sessionLogin(req) != "amitbet" {
		t.Fatalf("sessionLogin mismatch")
	}
	if srv.pushAuthUsername(req) != "amitbet" || srv.pushToken(req) != "user-token" {
		t.Fatalf("push helpers should prefer session token/login")
	}
}

func TestGetSessionAndSetupStatus(t *testing.T) {
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: filepath.Join(t.TempDir(), "root"),
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			Spaces:           []appsettings.SpaceEntry{{Key: "main", DisplayName: "Main"}},
		},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rr := httptest.NewRecorder()
	srv.getSession(rr, req)
	if strings.TrimSpace(rr.Body.String()) != "null" {
		t.Fatalf("getSession without cookie should return null, got %q", rr.Body.String())
	}

	sess := &session.Session{ID: "sid-2", Login: "amitbet", Name: "Amit", AvatarURL: "https://img"}
	srv.Sessions.Put(sess)
	req = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})
	rr = httptest.NewRecorder()
	srv.getSession(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\"login\":\"amitbet\"") {
		t.Fatalf("getSession with cookie unexpected response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rr = httptest.NewRecorder()
	srv.getSetupStatus(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\"configured\":true") {
		t.Fatalf("getSetupStatus unexpected response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetSettingsAndUpdateSettings(t *testing.T) {
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: filepath.Join(t.TempDir(), "root"),
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			SaveMode:         "git_sync",
			Spaces:           []appsettings.SpaceEntry{{Key: "main", DisplayName: "Main"}},
		},
	}
	srv := newTestServer(t, store)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.getSettings(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\"implementation\":\"repo_settings+local_runtime\"") {
		t.Fatalf("getSettings unexpected response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"save_mode":"local"}`))
	rr = httptest.NewRecorder()
	srv.updateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("updateSettings status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(store.saved) == 0 || store.saved[len(store.saved)-1].SaveMode != "local" {
		t.Fatalf("updateSettings should persist normalized save mode, saved=%#v", store.saved)
	}

	rootSettingsPath := srv.rootSettingsPath(store.load)
	b, err := os.ReadFile(rootSettingsPath)
	if err != nil {
		t.Fatalf("expected root settings file to be written: %v", err)
	}
	if !strings.Contains(string(b), "\"save_mode\": \"local\"") {
		t.Fatalf("root settings file missing updated save_mode: %s", string(b))
	}
}

func TestRequireSpaceCreator(t *testing.T) {
	srv := newTestServer(t, &fakeSettingsStore{})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	if _, ok := srv.requireSpaceCreator(rr, req, appsettings.SpaceEntry{CreatedBy: "amitbet"}); ok {
		t.Fatalf("requireSpaceCreator should reject missing login")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing login status = %d", rr.Code)
	}

	sess := &session.Session{ID: "sid-3", Login: "amitbet"}
	srv.Sessions.Put(sess)
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})

	rr = httptest.NewRecorder()
	if _, ok := srv.requireSpaceCreator(rr, req, appsettings.SpaceEntry{}); ok {
		t.Fatalf("requireSpaceCreator should reject spaces with no creator metadata")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no creator metadata status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	if _, ok := srv.requireSpaceCreator(rr, req, appsettings.SpaceEntry{CreatedBy: "someone-else"}); ok {
		t.Fatalf("requireSpaceCreator should reject different creator")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("different creator status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	login, ok := srv.requireSpaceCreator(rr, req, appsettings.SpaceEntry{CreatedBy: "AMITBET"})
	if !ok || login != "amitbet" {
		t.Fatalf("requireSpaceCreator should accept case-insensitive creator match: ok=%t login=%q", ok, login)
	}
}

func TestGetPageAndSavePageLocalMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if _, err := gitops.EnsureRepo(root); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: root,
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			SaveMode:         "local",
			Spaces:           []appsettings.SpaceEntry{{Key: "main", DisplayName: "Main", Path: "."}},
		},
	}
	srv := newTestServer(t, store)
	if _, err := ensureInitialized(root, "main"); err != nil {
		t.Fatalf("ensureInitialized: %v", err)
	}

	req := withURLParam(httptest.NewRequest(http.MethodGet, "/api/spaces/main/page?path=README.md", nil), "space", "main")
	rr := httptest.NewRecorder()
	srv.getPage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("getPage status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Start editing this page") {
		t.Fatalf("getPage should return default welcome content, body=%s", rr.Body.String())
	}

	req = withURLParam(httptest.NewRequest(http.MethodPost, "/api/spaces/main/page", strings.NewReader(`{"path":"docs/test","content":"# Hello"}`)), "space", "main")
	rr = httptest.NewRecorder()
	srv.savePage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("savePage status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"save_mode\":\"local\"") || !strings.Contains(rr.Body.String(), "\"path\":\"docs/test.md\"") {
		t.Fatalf("savePage response mismatch: %s", rr.Body.String())
	}

	b, err := os.ReadFile(filepath.Join(root, "docs", "test.md"))
	if err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	if string(b) != "# Hello" {
		t.Fatalf("saved file content = %q", string(b))
	}

	req = withURLParam(httptest.NewRequest(http.MethodGet, "/api/spaces/main/page?path=docs/test.md", nil), "space", "main")
	rr = httptest.NewRecorder()
	srv.getPage(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\"content\":\"# Hello\"") {
		t.Fatalf("getPage should return saved content: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePageAndListPagesLocalMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if _, err := gitops.EnsureRepo(root); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: root,
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			SaveMode:         "local",
			Spaces:           []appsettings.SpaceEntry{{Key: "main", DisplayName: "Main", Path: "."}},
		},
	}
	srv := newTestServer(t, store)
	if _, err := ensureInitialized(root, "main"); err != nil {
		t.Fatalf("ensureInitialized: %v", err)
	}

	req := withURLParam(httptest.NewRequest(http.MethodPost, "/api/spaces/main/pages", strings.NewReader(`{"path":"guide/start"}`)), "space", "main")
	rr := httptest.NewRecorder()
	srv.createPage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("createPage status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"path\":\"guide/start.md\"") {
		t.Fatalf("createPage response mismatch: %s", rr.Body.String())
	}

	if _, err := os.Stat(filepath.Join(root, "guide", "start.md")); err != nil {
		t.Fatalf("created page missing: %v", err)
	}

	req = withURLParam(httptest.NewRequest(http.MethodGet, "/api/spaces/main/pages", nil), "space", "main")
	rr = httptest.NewRecorder()
	srv.listPages(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("listPages status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"path\":\"guide/start.md\"") {
		t.Fatalf("listPages should include created page, body=%s", rr.Body.String())
	}
}

func TestCorsMiddleware(t *testing.T) {
	handler := cors("http://localhost:5173")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("cors origin header missing")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTeapot {
		t.Fatalf("GET status = %d", rr.Code)
	}
}
