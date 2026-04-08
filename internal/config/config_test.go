package config

import "testing"

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("MDWIKI_LISTEN", "")
	t.Setenv("MDWIKI_DATA", "")
	t.Setenv("MDWIKI_REGISTRY", "")
	t.Setenv("MDWIKI_SETTINGS_PATH", "")
	t.Setenv("MDWIKI_ROOT_GIT_REPO", "")
	t.Setenv("MDWIKI_ROOT_GIT_BRANCH", "")
	t.Setenv("MDWIKI_ROOT_LOCAL_DIR", "")
	t.Setenv("MDWIKI_STORAGE_DIR", "")
	t.Setenv("MDWIKI_SPACE_SETTINGS_FILE", "")
	t.Setenv("MDWIKI_GITHUB_CLIENT_ID", "")
	t.Setenv("MDWIKI_GITHUB_CLIENT_SECRET", "")
	t.Setenv("MDWIKI_GITHUB_CALLBACK", "")
	t.Setenv("MDWIKI_SESSION_SECRET", "")
	t.Setenv("MDWIKI_REDIS_URL", "")
	t.Setenv("MDWIKI_SERVER_GIT_TOKEN", "")
	t.Setenv("MDWIKI_FRONTEND_ORIGIN", "")

	cfg := FromEnv()
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.RegistryPath != "./spaces-registry.yaml" {
		t.Fatalf("RegistryPath = %q", cfg.RegistryPath)
	}
	if cfg.SettingsPath != "./data/settings.json" {
		t.Fatalf("SettingsPath = %q", cfg.SettingsPath)
	}
	if cfg.RootRepoURL != "https://github.com/amitbet/documents" {
		t.Fatalf("RootRepoURL = %q", cfg.RootRepoURL)
	}
	if cfg.RootRepoBranch != "main" {
		t.Fatalf("RootRepoBranch = %q", cfg.RootRepoBranch)
	}
	if cfg.RootRepoLocalDir != "/tmp/mdwiki/repos/root" {
		t.Fatalf("RootRepoLocalDir = %q", cfg.RootRepoLocalDir)
	}
	if cfg.StorageDir != "/tmp/mdwiki/state" {
		t.Fatalf("StorageDir = %q", cfg.StorageDir)
	}
	if cfg.SpaceSettingsFile != "mdwiki.spaces.json" {
		t.Fatalf("SpaceSettingsFile = %q", cfg.SpaceSettingsFile)
	}
	if cfg.GitHubCallbackURL != "http://localhost:8080/auth/github/callback" {
		t.Fatalf("GitHubCallbackURL = %q", cfg.GitHubCallbackURL)
	}
	if cfg.SessionSecret != "dev-insecure-change-me" {
		t.Fatalf("SessionSecret = %q", cfg.SessionSecret)
	}
	if cfg.FrontendOrigin != "http://localhost:5173" {
		t.Fatalf("FrontendOrigin = %q", cfg.FrontendOrigin)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("MDWIKI_LISTEN", ":9999")
	t.Setenv("MDWIKI_DATA", "/data")
	t.Setenv("MDWIKI_REGISTRY", "/registry.yaml")
	t.Setenv("MDWIKI_SETTINGS_PATH", "/settings.json")
	t.Setenv("MDWIKI_ROOT_GIT_REPO", "https://example.com/repo.git")
	t.Setenv("MDWIKI_ROOT_GIT_BRANCH", "develop")
	t.Setenv("MDWIKI_ROOT_LOCAL_DIR", "/repos/root")
	t.Setenv("MDWIKI_STORAGE_DIR", "/state")
	t.Setenv("MDWIKI_SPACE_SETTINGS_FILE", "spaces.json")
	t.Setenv("MDWIKI_GITHUB_CLIENT_ID", "cid")
	t.Setenv("MDWIKI_GITHUB_CLIENT_SECRET", "secret")
	t.Setenv("MDWIKI_GITHUB_CALLBACK", "http://callback")
	t.Setenv("MDWIKI_SESSION_SECRET", "session")
	t.Setenv("MDWIKI_REDIS_URL", "redis://localhost")
	t.Setenv("MDWIKI_SERVER_GIT_TOKEN", "token")
	t.Setenv("MDWIKI_FRONTEND_ORIGIN", "http://frontend")

	cfg := FromEnv()
	if cfg.ListenAddr != ":9999" || cfg.DataDir != "/data" || cfg.RegistryPath != "/registry.yaml" {
		t.Fatalf("unexpected overridden basics: %+v", cfg)
	}
	if cfg.SettingsPath != "/settings.json" || cfg.RootRepoURL != "https://example.com/repo.git" {
		t.Fatalf("unexpected overridden repo config: %+v", cfg)
	}
	if cfg.RootRepoBranch != "develop" || cfg.RootRepoLocalDir != "/repos/root" || cfg.StorageDir != "/state" {
		t.Fatalf("unexpected overridden dirs: %+v", cfg)
	}
	if cfg.SpaceSettingsFile != "spaces.json" || cfg.GitHubClientID != "cid" || cfg.GitHubSecret != "secret" {
		t.Fatalf("unexpected overridden oauth config: %+v", cfg)
	}
	if cfg.GitHubCallbackURL != "http://callback" || cfg.SessionSecret != "session" {
		t.Fatalf("unexpected overridden callback/session config: %+v", cfg)
	}
	if cfg.RedisURL != "redis://localhost" || cfg.ServerGitToken != "token" || cfg.FrontendOrigin != "http://frontend" {
		t.Fatalf("unexpected overridden integration config: %+v", cfg)
	}
}

func TestBool(t *testing.T) {
	t.Setenv("BOOL_TRUE", "true")
	t.Setenv("BOOL_FALSE", "false")
	t.Setenv("BOOL_BAD", "not-a-bool")
	t.Setenv("BOOL_EMPTY", "")

	if !Bool("BOOL_TRUE", false) {
		t.Fatalf("BOOL_TRUE should parse true")
	}
	if Bool("BOOL_FALSE", true) {
		t.Fatalf("BOOL_FALSE should parse false")
	}
	if !Bool("BOOL_BAD", true) {
		t.Fatalf("BOOL_BAD should fall back to default true")
	}
	if Bool("BOOL_EMPTY", false) {
		t.Fatalf("BOOL_EMPTY should use default false")
	}
}
