package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/comments"
	"mdwiki/internal/session"
)

func initGitMainRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test User",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "seed")
}

func newServerWithSpace(t *testing.T, saveMode string) (*Server, *fakeSettingsStore) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	initGitMainRepo(t, root)
	spaceRoot := filepath.Join(root, "spaces", "main")
	if err := os.MkdirAll(spaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir space root: %v", err)
	}
	store := &fakeSettingsStore{
		load: appsettings.Settings{
			RootRepoURL:      "https://github.com/amitbet/documents",
			RootRepoBranch:   "main",
			RootRepoLocalDir: root,
			StorageDir:       filepath.Join(t.TempDir(), "state"),
			SaveMode:         saveMode,
			Spaces: []appsettings.SpaceEntry{
				{Key: "main", DisplayName: "Main", Path: "spaces/main", Branch: "main"},
			},
		},
	}
	return newTestServer(t, store), store
}

func withURLParams(req *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func addSessionCookie(t *testing.T, srv *Server, req *http.Request, login, name string) {
	t.Helper()
	sess := &session.Session{ID: "sid-" + login, Login: login, Name: name, AccessToken: "user-token"}
	srv.Sessions.Put(sess)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})
}

func decodeJSONBody(t *testing.T, rr *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatalf("decode json body %q: %v", rr.Body.String(), err)
	}
}

func TestCommentHandlersLocalLifecycle(t *testing.T) {
	srv, store := newServerWithSpace(t, "local")
	root := filepath.Join(store.load.RootRepoLocalDir, "spaces", "main")

	addReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/comments", strings.NewReader(`{"path":"docs/page.md","anchor_id":"anchor-1","comment":"First note","position":2}`))
	addReq = withURLParam(addReq, "space", "main")
	addSessionCookie(t, srv, addReq, "amit", "Amit")
	addRR := httptest.NewRecorder()
	srv.addComment(addRR, addReq)
	if addRR.Code != http.StatusOK {
		t.Fatalf("addComment status = %d body=%s", addRR.Code, addRR.Body.String())
	}

	var addResp struct {
		ThreadID string `json:"thread_id"`
		Message  struct {
			HashID   string `json:"hash_id"`
			AuthorID string `json:"author_id"`
			Body     string `json:"body"`
		} `json:"message"`
	}
	decodeJSONBody(t, addRR, &addResp)
	if addResp.ThreadID != "anchor-1" || addResp.Message.AuthorID != "amit" || addResp.Message.Body != "First note" {
		t.Fatalf("unexpected addComment response: %+v", addResp)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/spaces/main/comments?path=docs/page.md", nil)
	listReq = withURLParam(listReq, "space", "main")
	addSessionCookie(t, srv, listReq, "amit", "Amit")
	listRR := httptest.NewRecorder()
	srv.listComments(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("listComments status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listResp struct {
		Comments []struct {
			ThreadID string `json:"thread_id"`
			AnchorID string `json:"anchor_id"`
			Messages []struct {
				Body    string `json:"body"`
				CanEdit bool   `json:"can_edit"`
			} `json:"messages"`
		} `json:"comments"`
	}
	decodeJSONBody(t, listRR, &listResp)
	if len(listResp.Comments) != 1 || !listResp.Comments[0].Messages[0].CanEdit {
		t.Fatalf("unexpected listComments response: %+v", listResp)
	}

	replyReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/comments/anchor-1/reply", strings.NewReader(`{"path":"docs/page.md","comment":"Reply","position":3,"in_reply_to":"`+addResp.Message.HashID+`"}`))
	replyReq = withURLParams(replyReq, map[string]string{"space": "main", "thread": "anchor-1"})
	addSessionCookie(t, srv, replyReq, "amit", "Amit")
	replyRR := httptest.NewRecorder()
	srv.replyComment(replyRR, replyReq)
	if replyRR.Code != http.StatusOK {
		t.Fatalf("replyComment status = %d body=%s", replyRR.Code, replyRR.Body.String())
	}

	editReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/comments/anchor-1/edit", strings.NewReader(`{"path":"docs/page.md","hash_id":"`+addResp.Message.HashID+`","comment":"Edited note","position":4}`))
	editReq = withURLParams(editReq, map[string]string{"space": "main", "thread": "anchor-1"})
	addSessionCookie(t, srv, editReq, "amit", "Amit")
	editRR := httptest.NewRecorder()
	srv.editComment(editRR, editReq)
	if editRR.Code != http.StatusOK {
		t.Fatalf("editComment status = %d body=%s", editRR.Code, editRR.Body.String())
	}

	commentPath := filepath.Join(root, ".mdwiki", "comments", comments.PageKey("docs/page.md"), "anchor-1.json")
	data, err := os.ReadFile(commentPath)
	if err != nil {
		t.Fatalf("read thread file: %v", err)
	}
	var tf comments.ThreadFile
	if err := json.Unmarshal(data, &tf); err != nil {
		t.Fatalf("unmarshal thread file: %v", err)
	}
	if len(tf.Messages) != 3 {
		t.Fatalf("expected 3 messages after reply+edit, got %d", len(tf.Messages))
	}
	if tf.Messages[1].InReplyTo == nil || *tf.Messages[1].InReplyTo != addResp.Message.HashID {
		t.Fatalf("reply should preserve in_reply_to: %+v", tf.Messages[1])
	}
	if tf.Messages[2].Replaces == nil || *tf.Messages[2].Replaces != addResp.Message.HashID {
		t.Fatalf("edit should set replaces: %+v", tf.Messages[2])
	}

	resolveReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/comments/anchor-1/resolve", strings.NewReader(`{"path":"docs/page.md"}`))
	resolveReq = withURLParams(resolveReq, map[string]string{"space": "main", "thread": "anchor-1"})
	addSessionCookie(t, srv, resolveReq, "amit", "Amit")
	resolveRR := httptest.NewRecorder()
	srv.resolveComment(resolveRR, resolveReq)
	if resolveRR.Code != http.StatusOK {
		t.Fatalf("resolveComment status = %d body=%s", resolveRR.Code, resolveRR.Body.String())
	}
	if _, err := os.Stat(commentPath); !os.IsNotExist(err) {
		t.Fatalf("comment thread should be deleted, err=%v", err)
	}
}

func TestDraftHandlersSaveAndGet(t *testing.T) {
	srv, store := newServerWithSpace(t, "git_sync")
	root := filepath.Join(store.load.RootRepoLocalDir, "spaces", "main")

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "page.md"), []byte("# Page\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cmd := exec.Command("git", "-C", store.load.RootRepoLocalDir, "add", ".")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", store.load.RootRepoLocalDir, "commit", "-m", "add page")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}

	saveReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/draft", strings.NewReader(`{"path":"docs/page","markdown":"# Draft body","update_b64":"abc123"}`))
	saveReq = withURLParam(saveReq, "space", "main")
	addSessionCookie(t, srv, saveReq, "amit", "Amit")
	saveRR := httptest.NewRecorder()
	srv.saveDraft(saveRR, saveReq)
	if saveRR.Code != http.StatusOK {
		t.Fatalf("saveDraft status = %d body=%s", saveRR.Code, saveRR.Body.String())
	}
	var saveResp map[string]any
	decodeJSONBody(t, saveRR, &saveResp)
	if saveResp["ok"] != true {
		t.Fatalf("unexpected saveDraft response: %+v", saveResp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/spaces/main/draft?path=docs/page", nil)
	getReq = withURLParam(getReq, "space", "main")
	addSessionCookie(t, srv, getReq, "amit", "Amit")
	getRR := httptest.NewRecorder()
	srv.getDraft(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("getDraft status = %d body=%s", getRR.Code, getRR.Body.String())
	}

	var getResp draftResponse
	decodeJSONBody(t, getRR, &getResp)
	if !getResp.Exists || getResp.Path != "docs/page.md" || getResp.Format != "yjs" || getResp.Markdown != "# Draft body" {
		t.Fatalf("unexpected getDraft response: %+v", getResp)
	}
	if getResp.User != "amit" || getResp.Space != "main" || getResp.UpdateB64 != "abc123" {
		t.Fatalf("draft metadata mismatch: %+v", getResp)
	}

	repoRoot, err := repoRootForSpace(root)
	if err != nil {
		t.Fatalf("repoRootForSpace: %v", err)
	}
	rel := draftRelPath("amit", "main", "docs/page.md")
	if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
		t.Fatalf("draft file missing: %v", err)
	}
}

func TestMediaHandlersLifecycle(t *testing.T) {
	srv, store := newServerWithSpace(t, "git_sync")
	root := filepath.Join(store.load.RootRepoLocalDir, "spaces", "main")

	createReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/diagrams", strings.NewReader(`{"kind":"excalidraw","name":"Architecture Board"}`))
	createReq = withURLParam(createReq, "space", "main")
	createRR := httptest.NewRecorder()
	srv.createDiagram(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("createDiagram status = %d body=%s", createRR.Code, createRR.Body.String())
	}

	var createResp struct {
		OK      bool   `json:"ok"`
		Path    string `json:"path"`
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	decodeJSONBody(t, createRR, &createResp)
	if !createResp.OK || createResp.Kind != "excalidraw" || !strings.HasSuffix(createResp.Path, "Architecture-Board.excalidraw") {
		t.Fatalf("unexpected createDiagram response: %+v", createResp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/spaces/main/diagram?path="+createResp.Path, nil)
	getReq = withURLParam(getReq, "space", "main")
	getReq.URL.RawQuery = "path=" + createResp.Path
	getRR := httptest.NewRecorder()
	srv.getDiagram(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("getDiagram status = %d body=%s", getRR.Code, getRR.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/diagram", strings.NewReader(`{"path":"`+createResp.Path+`","content":"updated"}`))
	updateReq = withURLParam(updateReq, "space", "main")
	updateRR := httptest.NewRecorder()
	srv.updateDiagram(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("updateDiagram status = %d body=%s", updateRR.Code, updateRR.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/api/spaces/main/asset", nil)
	assetReq = withURLParam(assetReq, "space", "main")
	assetReq.URL.RawQuery = "path=" + createResp.Path
	assetRR := httptest.NewRecorder()
	srv.assetFile(assetRR, assetReq)
	if assetRR.Code != http.StatusOK {
		t.Fatalf("assetFile status = %d body=%s", assetRR.Code, assetRR.Body.String())
	}
	if body := assetRR.Body.String(); body != "updated" {
		t.Fatalf("assetFile body = %q", body)
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "diagram preview?.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("pngdata")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/spaces/main/assets/image", &form)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq = withURLParam(uploadReq, "space", "main")
	uploadRR := httptest.NewRecorder()
	srv.uploadImageAsset(uploadRR, uploadReq)
	if uploadRR.Code != http.StatusOK {
		t.Fatalf("uploadImageAsset status = %d body=%s", uploadRR.Code, uploadRR.Body.String())
	}

	var uploadResp struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	decodeJSONBody(t, uploadRR, &uploadResp)
	if !uploadResp.OK || !strings.HasPrefix(uploadResp.Path, ".mdwiki/assets/images/") || !strings.HasSuffix(uploadResp.Path, "diagram-preview-.png") {
		t.Fatalf("unexpected uploadImageAsset response: %+v", uploadResp)
	}
	data, err := os.ReadFile(filepath.Join(root, uploadResp.Path))
	if err != nil {
		t.Fatalf("read uploaded asset: %v", err)
	}
	if string(data) != "pngdata" {
		t.Fatalf("uploaded asset mismatch: %q", string(data))
	}
}

func TestListCommentsMissingPathAndUnknownSpace(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")

	missingPathReq := httptest.NewRequest(http.MethodGet, "/api/spaces/main/comments", nil)
	missingPathReq = withURLParam(missingPathReq, "space", "main")
	missingPathRR := httptest.NewRecorder()
	srv.listComments(missingPathRR, missingPathReq)
	if missingPathRR.Code != http.StatusBadRequest {
		t.Fatalf("missing path status = %d", missingPathRR.Code)
	}

	unknownReq := httptest.NewRequest(http.MethodGet, "/api/spaces/other/comments?path=docs/page.md", nil)
	unknownReq = withURLParam(unknownReq, "space", "other")
	unknownReq.URL.RawQuery = "path=docs/page.md"
	unknownRR := httptest.NewRecorder()
	srv.listComments(unknownRR, unknownReq)
	if unknownRR.Code != http.StatusNotFound {
		t.Fatalf("unknown space status = %d", unknownRR.Code)
	}
}

func TestDraftOwnerFromRequestFallsBackToLocal(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := draftOwnerFromRequest(srv, req); got != "local" {
		t.Fatalf("draftOwnerFromRequest = %q, want local", got)
	}
}

func TestServerLoadSettingsStillWorksInHandlerHelpers(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	if _, err := srv.loadSettings(context.Background()); err != nil {
		t.Fatalf("loadSettings unexpected error: %v", err)
	}
}
