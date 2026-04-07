package config

import (
	"os"
	"strconv"
)

// Config holds server configuration.
type Config struct {
	ListenAddr        string
	DataDir           string
	RegistryPath      string // spaces-registry.yaml
	SettingsPath      string // local JSON storage for root repo + spaces
	RootRepoURL       string // default root git repo URL
	RootRepoBranch    string
	RootRepoLocalDir  string // local clone directory for root repo
	StorageDir        string // local storage root for server data
	SpaceSettingsFile string // settings JSON name in root repo
	GitHubClientID    string
	GitHubSecret      string
	GitHubCallbackURL string
	SessionSecret     string
	RedisURL          string // empty = single-instance mode
	ServerGitToken    string // PAT or app token for clone/fallback push
	FrontendOrigin    string // CORS
}

func FromEnv() Config {
	return Config{
		ListenAddr:        get("MDWIKI_LISTEN", ":8080"),
		DataDir:           get("MDWIKI_DATA", "./data"),
		RegistryPath:      get("MDWIKI_REGISTRY", "./spaces-registry.yaml"),
		SettingsPath:      get("MDWIKI_SETTINGS_PATH", "./data/settings.json"),
		RootRepoURL:       get("MDWIKI_ROOT_GIT_REPO", "https://github.com/amitbet/documents"),
		RootRepoBranch:    get("MDWIKI_ROOT_GIT_BRANCH", "main"),
		RootRepoLocalDir:  get("MDWIKI_ROOT_LOCAL_DIR", "./data/root-git-repo"),
		StorageDir:        get("MDWIKI_STORAGE_DIR", "./data/storage"),
		SpaceSettingsFile: get("MDWIKI_SPACE_SETTINGS_FILE", "mdwiki.spaces.json"),
		GitHubClientID:    os.Getenv("MDWIKI_GITHUB_CLIENT_ID"),
		GitHubSecret:      os.Getenv("MDWIKI_GITHUB_CLIENT_SECRET"),
		GitHubCallbackURL: get("MDWIKI_GITHUB_CALLBACK", "http://localhost:8080/auth/github/callback"),
		SessionSecret:     get("MDWIKI_SESSION_SECRET", "dev-insecure-change-me"),
		RedisURL:          os.Getenv("MDWIKI_REDIS_URL"),
		ServerGitToken:    os.Getenv("MDWIKI_SERVER_GIT_TOKEN"),
		FrontendOrigin:    get("MDWIKI_FRONTEND_ORIGIN", "http://localhost:5173"),
	}
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Bool(k string, def bool) bool {
	s := os.Getenv(k)
	if s == "" {
		return def
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return b
}
