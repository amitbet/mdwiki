package appsettings

import "context"

// SpaceEntry defines a wiki space under the configured root repository.
type SpaceEntry struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	CreatedBy   string `json:"created_by_login,omitempty"`
	Path        string `json:"path"`
	RepoURL     string `json:"repo_url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	LocalDir    string `json:"local_dir,omitempty"`
}

// Settings is the persisted app state.
type Settings struct {
	RootRepoURL      string       `json:"root_repo_url"`
	RootRepoBranch   string       `json:"root_repo_branch,omitempty"`
	RootRepoLocalDir string       `json:"root_repo_local_dir"`
	StorageDir       string       `json:"storage_dir"`
	SaveMode         string       `json:"save_mode,omitempty"` // local | git_sync
	Spaces           []SpaceEntry `json:"spaces"`
}

// Store abstracts settings persistence.
type Store interface {
	Load(context.Context) (Settings, error)
	Save(context.Context, Settings) error
}
