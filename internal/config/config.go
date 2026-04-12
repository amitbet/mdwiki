package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds server configuration.
type Config struct {
	ListenAddr        string
	DataDir           string
	RegistryPath      string // spaces-registry.yaml
	SettingsPath      string // local JSON storage for node-local runtime overrides
	RootRepoURL       string // default root git repo URL
	RootRepoBranch    string
	RootRepoLocalDir  string // local clone directory for root repo
	StorageDir        string // local storage root for server data
	SpaceSettingsFile string // settings JSON name in root repo
	GitHubClientID    string
	GitHubSecret      string
	GitHubCallbackURL string
	SessionSecret     string
	RedisEnabled      bool
	RedisURL          string // empty = single-instance mode
	RedisAddrs        []string
	RedisClusterMode  bool
	RedisUsername     string
	RedisPassword     string
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
		RootRepoLocalDir:  get("MDWIKI_ROOT_LOCAL_DIR", "/tmp/mdwiki/repos/root"),
		StorageDir:        get("MDWIKI_STORAGE_DIR", "/tmp/mdwiki/state"),
		SpaceSettingsFile: get("MDWIKI_SPACE_SETTINGS_FILE", "mdwiki.spaces.json"),
		GitHubClientID:    os.Getenv("MDWIKI_GITHUB_CLIENT_ID"),
		GitHubSecret:      os.Getenv("MDWIKI_GITHUB_CLIENT_SECRET"),
		GitHubCallbackURL: get("MDWIKI_GITHUB_CALLBACK", "http://localhost:8080/auth/github/callback"),
		SessionSecret:     get("MDWIKI_SESSION_SECRET", "dev-insecure-change-me"),
		RedisEnabled:      Bool("MDWIKI_REDIS_ENABLED", false),
		RedisURL:          os.Getenv("MDWIKI_REDIS_URL"),
		RedisAddrs:        splitCSV(os.Getenv("MDWIKI_REDIS_ADDRS")),
		RedisClusterMode:  Bool("MDWIKI_REDIS_CLUSTER_MODE", false),
		RedisUsername:     os.Getenv("MDWIKI_REDIS_USERNAME"),
		RedisPassword:     os.Getenv("MDWIKI_REDIS_PASSWORD"),
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

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
