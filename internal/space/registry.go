package space

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Registry maps space keys to repo roots (mount-style).
type Registry struct {
	Spaces []SpaceEntry `yaml:"spaces"`
}

type SpaceEntry struct {
	Key         string `yaml:"key"`
	DisplayName string `yaml:"display_name"`
	RepoURL     string `yaml:"repo_url"` // https clone URL
	Branch      string `yaml:"branch"`
	LocalPath   string `yaml:"local_path"` // optional override under data dir
}

func LoadRegistry(path string) (*Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ResolveRoot returns absolute filesystem root for a space.
func (r *Registry) ResolveRoot(dataDir, key string) (string, *SpaceEntry, bool) {
	for i := range r.Spaces {
		if r.Spaces[i].Key != key {
			continue
		}
		e := &r.Spaces[i]
		if e.LocalPath != "" {
			if filepath.IsAbs(e.LocalPath) {
				return e.LocalPath, e, true
			}
			return filepath.Join(dataDir, "spaces", e.LocalPath), e, true
		}
		return filepath.Join(dataDir, "spaces", key), e, true
	}
	return "", nil, false
}
