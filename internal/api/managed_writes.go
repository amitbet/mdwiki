package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mdwiki/internal/gitops"
	"mdwiki/internal/indexbuilder"
	"mdwiki/internal/session"
)

type writeContext struct {
	saveMode    string
	branch      string
	authorName  string
	authorEmail string
	pushUser    string
	pushToken   string
}

func (s *Server) writeContextForRequest(ctx context.Context, r *http.Request, entBranch string) (writeContext, error) {
	cfg, err := s.loadSettings(ctx)
	if err != nil {
		return writeContext{}, err
	}
	branch := strings.TrimSpace(entBranch)
	if branch == "" {
		branch = strings.TrimSpace(cfg.RootRepoBranch)
	}
	if branch == "" {
		branch = "main"
	}
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Name) != "" {
				authorName = sess.Name
			} else if strings.TrimSpace(sess.Login) != "" {
				authorName = sess.Login
			}
			if strings.TrimSpace(sess.Login) != "" {
				authorEmail = sess.Login + "@users.noreply.github.com"
			}
		}
	}
	return writeContext{
		saveMode:    normalizeSaveMode(cfg.SaveMode),
		branch:      branch,
		authorName:  authorName,
		authorEmail: authorEmail,
		pushUser:    s.pushAuthUsername(r),
		pushToken:   s.pushToken(r),
	}, nil
}

func (s *Server) writeManagedFile(ctx context.Context, req *http.Request, spaceRoot, entBranch, relPath, content, commitMsg string) error {
	wc, err := s.writeContextForRequest(ctx, req, entBranch)
	if err != nil {
		return err
	}
	if wc.saveMode == "local" {
		return gitops.WriteFileOnly(spaceRoot, relPath, content)
	}
	repoRoot, repoRelPath, err := resolveRepoPath(spaceRoot, relPath)
	if err != nil {
		return err
	}
	_, err = s.executeGitWrite(ctx, gitWriteJob{
		ID:          session.NewID(),
		Op:          "save",
		RepoRoot:    repoRoot,
		Branch:      wc.branch,
		Path:        repoRelPath,
		Content:     content,
		CommitMsg:   commitMsg,
		AuthorName:  wc.authorName,
		AuthorEmail: wc.authorEmail,
		PushUser:    wc.pushUser,
		PushToken:   wc.pushToken,
		StrictPush:  true,
	})
	return err
}

func (s *Server) renameManagedFile(ctx context.Context, req *http.Request, spaceRoot, entBranch, oldRelPath, newRelPath string) error {
	wc, err := s.writeContextForRequest(ctx, req, entBranch)
	if err != nil {
		return err
	}
	if wc.saveMode == "local" {
		oldFull := filepath.Join(spaceRoot, filepath.FromSlash(oldRelPath))
		newFull := filepath.Join(spaceRoot, filepath.FromSlash(newRelPath))
		if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
			return err
		}
		return os.Rename(oldFull, newFull)
	}
	repoRoot, repoRelOld, err := resolveRepoPath(spaceRoot, oldRelPath)
	if err != nil {
		return err
	}
	repoRootTo, repoRelNew, err := resolveRepoPath(spaceRoot, newRelPath)
	if err != nil {
		return err
	}
	if repoRoot != repoRootTo {
		return errors.New("cannot rename across repositories")
	}
	_, err = s.executeGitWrite(ctx, gitWriteJob{
		ID:          session.NewID(),
		Op:          "rename",
		RepoRoot:    repoRoot,
		Branch:      wc.branch,
		FromPath:    repoRelOld,
		ToPath:      repoRelNew,
		AuthorName:  wc.authorName,
		AuthorEmail: wc.authorEmail,
		PushUser:    wc.pushUser,
		PushToken:   wc.pushToken,
		StrictPush:  true,
	})
	return err
}

func (s *Server) deleteManagedFile(ctx context.Context, req *http.Request, spaceRoot, entBranch, relPath string) error {
	wc, err := s.writeContextForRequest(ctx, req, entBranch)
	if err != nil {
		return err
	}
	if wc.saveMode == "local" {
		return os.Remove(filepath.Join(spaceRoot, filepath.FromSlash(relPath)))
	}
	repoRoot, repoRelPath, err := resolveRepoPath(spaceRoot, relPath)
	if err != nil {
		return err
	}
	_, err = s.executeGitWrite(ctx, gitWriteJob{
		ID:          session.NewID(),
		Op:          "delete",
		RepoRoot:    repoRoot,
		Branch:      wc.branch,
		Path:        repoRelPath,
		AuthorName:  wc.authorName,
		AuthorEmail: wc.authorEmail,
		PushUser:    wc.pushUser,
		PushToken:   wc.pushToken,
		StrictPush:  true,
	})
	return err
}

func (s *Server) syncIndexFile(ctx context.Context, req *http.Request, spaceRoot, spaceKey, entBranch string) (*indexbuilder.IndexDoc, error) {
	doc, err := indexbuilder.ScanMarkdown(spaceRoot, spaceKey)
	if err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := s.writeManagedFile(ctx, req, spaceRoot, entBranch, ".mdwiki/index.json", string(raw)+"\n", "wiki: update routing index"); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Server) writeIndexDoc(ctx context.Context, req *http.Request, spaceRoot, entBranch string, doc *indexbuilder.IndexDoc, commitMsg string) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return s.writeManagedFile(ctx, req, spaceRoot, entBranch, ".mdwiki/index.json", string(raw)+"\n", commitMsg)
}
