package appsettings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFileStoreLoadMissingReturnsEmpty(t *testing.T) {
	store := NewLocalFileStore(filepath.Join(t.TempDir(), "settings.json"))
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load unexpected error: %v", err)
	}
	if got.RootRepoLocalDir != "" || got.StorageDir != "" {
		t.Fatalf("Load returned unexpected settings: %+v", got)
	}
}

func TestLocalFileStoreSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	store := NewLocalFileStore(path)
	want := Settings{
		RootRepoLocalDir: "/tmp/mdwiki/repos/root",
		StorageDir:       "/tmp/mdwiki/state",
		RootRepoURL:      "https://example.invalid/ignored",
		SaveMode:         "git_sync",
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("Save unexpected error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile unexpected error: %v", err)
	}
	if string(raw) == "" || raw[len(raw)-1] != '\n' {
		t.Fatalf("expected saved file to end with newline, got %q", string(raw))
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load unexpected error: %v", err)
	}
	if got.RootRepoLocalDir != want.RootRepoLocalDir || got.StorageDir != want.StorageDir {
		t.Fatalf("Load mismatch: got %+v want %+v", got, want)
	}
	if got.RootRepoURL != "" || got.SaveMode != "" {
		t.Fatalf("Local runtime store should not persist canonical settings: %+v", got)
	}
}
