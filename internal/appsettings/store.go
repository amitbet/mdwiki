package appsettings

import "context"

// SpaceEntry defines a wiki space under the configured root repository.
type SpaceEntry struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
}

// Settings is the persisted app state.
type Settings struct {
	RootRepoPath string       `json:"root_repo_path"`
	Spaces       []SpaceEntry `json:"spaces"`
}

// Store abstracts settings persistence.
type Store interface {
	Load(context.Context) (Settings, error)
	Save(context.Context, Settings) error
}
