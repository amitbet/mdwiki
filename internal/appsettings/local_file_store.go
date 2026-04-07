package appsettings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type localRuntimeSettings struct {
	RootRepoLocalDir string `json:"root_repo_local_dir"`
	StorageDir       string `json:"storage_dir"`
}

// LocalFileStore persists node-local runtime settings to a JSON file.
// Canonical wiki settings live in the root repo settings file.
type LocalFileStore struct {
	path string
	mu   sync.Mutex
}

func NewLocalFileStore(path string) *LocalFileStore {
	return &LocalFileStore{path: path}
}

func (s *LocalFileStore) Load(_ context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	if len(b) == 0 {
		return Settings{}, nil
	}
	var runtime localRuntimeSettings
	if err := json.Unmarshal(b, &runtime); err != nil {
		return Settings{}, err
	}
	return Settings{
		RootRepoLocalDir: runtime.RootRepoLocalDir,
		StorageDir:       runtime.StorageDir,
	}, nil
}

func (s *LocalFileStore) Save(_ context.Context, cfg Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(localRuntimeSettings{
		RootRepoLocalDir: cfg.RootRepoLocalDir,
		StorageDir:       cfg.StorageDir,
	}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.path, b, 0o644)
}
