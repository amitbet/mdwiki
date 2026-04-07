package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/gitops"
)

// GET /api/spaces/{space}/git — read-only git snapshot (branch, status, recent log).
func (s *Server) gitConsole(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, ent, ok := s.Registry.ResolveRoot(s.Cfg.DataDir, spaceKey)
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	tok := s.pushToken(r)
	if tok == "" {
		http.Error(w, "no git token", http.StatusForbidden)
		return
	}
	if _, err := gitops.EnsureClone(root, ent.RepoURL, ent.Branch, tok); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := gitConsoleSnapshot(root)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"output":       out,
		"repo_url":     ent.RepoURL,
		"branch":       ent.Branch,
		"space_key":    spaceKey,
		"display_name": ent.DisplayName,
	})
}

// GitHeadShort returns current HEAD short hash, or empty on failure.
func GitHeadShort(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func gitConsoleSnapshot(root string) string {
	var b strings.Builder
	b.WriteString(gitSection("branch", root, []string{"rev-parse", "--abbrev-ref", "HEAD"}))
	b.WriteString("\n")
	b.WriteString(gitSection("status", root, []string{"status", "--short", "-b"}))
	b.WriteString("\n")
	b.WriteString(gitSection("recent commits", root, []string{"log", "--oneline", "-20", "--decorate"}))
	return b.String()
}

func gitSection(title, root string, args []string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if stderr.Len() > 0 {
		if out != "" {
			out += "\n"
		}
		out += strings.TrimSpace(stderr.String())
	}
	if err != nil {
		return fmt.Sprintf("=== %s ===\n(error: %v)\n%s\n", title, err, out)
	}
	if out == "" {
		out = "(empty)"
	}
	return fmt.Sprintf("=== %s ===\n%s\n", title, out)
}
