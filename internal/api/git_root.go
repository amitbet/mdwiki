package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveRepoPath maps a path relative to a space root into the containing git repo root.
// It prefers the nearest git root to avoid hijacking writes to an unrelated parent repo.
func resolveRepoPath(spaceRoot, relPath string) (repoRoot, repoRelPath string, err error) {
	absRoot, err := filepath.Abs(spaceRoot)
	if err != nil {
		return "", "", err
	}
	full := filepath.Join(absRoot, filepath.FromSlash(relPath))

	root, err := nearestGitRoot(absRoot)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return "", "", fmt.Errorf("invalid repository-relative path: %s", rel)
	}
	return root, rel, nil
}

func repoRootForSpace(spaceRoot string) (string, error) {
	absRoot, err := filepath.Abs(spaceRoot)
	if err != nil {
		return "", err
	}
	return nearestGitRoot(absRoot)
}

func nearestGitRoot(start string) (string, error) {
	cur := start
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "", fmt.Errorf("no git repository found for %s", start)
}
