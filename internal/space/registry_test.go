package space

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistryAndResolveRoot(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "spaces.yaml")
	raw := []byte(`spaces:
  - key: main
    display_name: Main Space
  - key: docs
    display_name: Docs
    local_path: custom/docs
  - key: abs
    display_name: Abs
    local_path: /tmp/mdwiki-abs-space
`)
	if err := os.WriteFile(regPath, raw, 0o644); err != nil {
		t.Fatalf("WriteFile unexpected error: %v", err)
	}
	reg, err := LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry unexpected error: %v", err)
	}
	if len(reg.Spaces) != 3 {
		t.Fatalf("expected 3 spaces, got %d", len(reg.Spaces))
	}

	root, ent, ok := reg.ResolveRoot("/srv/mdwiki", "main")
	if !ok || ent == nil || root != filepath.Join("/srv/mdwiki", "spaces", "main") {
		t.Fatalf("ResolveRoot main mismatch: ok=%v ent=%+v root=%q", ok, ent, root)
	}

	root, ent, ok = reg.ResolveRoot("/srv/mdwiki", "docs")
	if !ok || ent == nil || root != filepath.Join("/srv/mdwiki", "spaces", "custom/docs") {
		t.Fatalf("ResolveRoot docs mismatch: ok=%v ent=%+v root=%q", ok, ent, root)
	}

	root, ent, ok = reg.ResolveRoot("/srv/mdwiki", "abs")
	if !ok || ent == nil || root != "/tmp/mdwiki-abs-space" {
		t.Fatalf("ResolveRoot abs mismatch: ok=%v ent=%+v root=%q", ok, ent, root)
	}

	if root, ent, ok = reg.ResolveRoot("/srv/mdwiki", "missing"); ok || ent != nil || root != "" {
		t.Fatalf("ResolveRoot missing mismatch: ok=%v ent=%+v root=%q", ok, ent, root)
	}
}
