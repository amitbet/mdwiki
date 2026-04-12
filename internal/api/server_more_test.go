package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mdwiki/internal/session"
)

type apiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f apiRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withAPITestHTTPClient(t *testing.T, fn apiRoundTripFunc) {
	t.Helper()
	prev := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fn}
	t.Cleanup(func() {
		http.DefaultClient = prev
	})
}

func apiHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRouterHealthAndSessionEndpoints(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	router := srv.Router()

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRR := httptest.NewRecorder()
	router.ServeHTTP(healthRR, healthReq)
	if healthRR.Code != http.StatusOK || healthRR.Body.String() != "ok" {
		t.Fatalf("health response = %d %q", healthRR.Code, healthRR.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	sessionRR := httptest.NewRecorder()
	router.ServeHTTP(sessionRR, sessionReq)
	if sessionRR.Code != http.StatusOK || strings.TrimSpace(sessionRR.Body.String()) != "null" {
		t.Fatalf("anonymous session response = %d %q", sessionRR.Code, sessionRR.Body.String())
	}

	sess := &session.Session{ID: "sid-1", Login: "amit", Name: "Amit", AvatarURL: "https://example.com/avatar.png"}
	srv.Sessions.Put(sess)
	sessionReq.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})
	sessionRR = httptest.NewRecorder()
	router.ServeHTTP(sessionRR, sessionReq)
	var got map[string]string
	decodeJSONBody(t, sessionRR, &got)
	if got["login"] != "amit" || got["name"] != "Amit" {
		t.Fatalf("session payload mismatch: %+v", got)
	}
}

func TestRequireSessionAndGitJobFallback(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")

	protected := srv.requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("requireSession anonymous status = %d", rr.Code)
	}

	sess := &session.Session{ID: "sid-2", Login: "amit"}
	srv.Sessions.Put(sess)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})
	rr = httptest.NewRecorder()
	protected.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("requireSession authenticated status = %d", rr.Code)
	}

	jobReq := httptest.NewRequest(http.MethodGet, "/api/git-jobs/123", nil)
	jobReq = withURLParam(jobReq, "jobID", "123")
	jobRR := httptest.NewRecorder()
	srv.getGitJob(jobRR, jobReq)
	if jobRR.Code != http.StatusNotFound {
		t.Fatalf("getGitJob without redis status = %d", jobRR.Code)
	}
}

func TestGitHubStartAndDeviceFlowHandlers(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	srv.oauth.ClientID = "client-123"
	srv.oauth.ClientSecret = "secret-123"
	srv.oauth.RedirectURL = "http://localhost/callback"

	startReq := httptest.NewRequest(http.MethodGet, "/auth/github", nil)
	startRR := httptest.NewRecorder()
	srv.githubStart(startRR, startReq)
	if startRR.Code != http.StatusFound {
		t.Fatalf("githubStart status = %d", startRR.Code)
	}
	location := startRR.Header().Get("Location")
	if !strings.Contains(location, "client_id=client-123") || !strings.Contains(location, "scope=read%3Auser+user%3Aemail+repo") {
		t.Fatalf("githubStart redirect mismatch: %s", location)
	}

	withAPITestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://github.com/login/device/code":
			return apiHTTPResponse(http.StatusOK, `{"device_code":"device-123","user_code":"USER-CODE","verification_uri":"https://github.com/login/device","verification_uri_complete":"https://github.com/login/device?user_code=USER-CODE","expires_in":900,"interval":5}`), nil
		case "https://github.com/login/oauth/access_token":
			return apiHTTPResponse(http.StatusOK, `{"access_token":"token-123","token_type":"bearer","expires_in":60}`), nil
		case "https://api.github.com/user":
			return apiHTTPResponse(http.StatusOK, `{"id":42,"login":"amit","name":"Amit","avatar_url":"https://example.com/a.png"}`), nil
		default:
			t.Fatalf("unexpected URL: %s", req.URL.String())
			return nil, nil
		}
	})

	deviceStartReq := httptest.NewRequest(http.MethodPost, "/auth/github/device/start", nil)
	deviceStartRR := httptest.NewRecorder()
	srv.githubDeviceStart(deviceStartRR, deviceStartReq)
	if deviceStartRR.Code != http.StatusOK {
		t.Fatalf("githubDeviceStart status = %d body=%s", deviceStartRR.Code, deviceStartRR.Body.String())
	}
	var startResp map[string]any
	decodeJSONBody(t, deviceStartRR, &startResp)
	flowID, _ := startResp["flow_id"].(string)
	if flowID == "" {
		t.Fatalf("missing flow id: %+v", startResp)
	}

	pollReq := httptest.NewRequest(http.MethodGet, "/auth/github/device/poll?flow_id="+flowID, nil)
	pollReq.URL.RawQuery = "flow_id=" + flowID
	pollRR := httptest.NewRecorder()
	srv.githubDevicePoll(pollRR, pollReq)
	if pollRR.Code != http.StatusOK {
		t.Fatalf("githubDevicePoll status = %d body=%s", pollRR.Code, pollRR.Body.String())
	}
	var pollResp map[string]any
	decodeJSONBody(t, pollRR, &pollResp)
	if pollResp["status"] != "complete" || pollResp["login"] != "amit" {
		t.Fatalf("unexpected githubDevicePoll response: %+v", pollResp)
	}
	if len(pollRR.Result().Cookies()) == 0 {
		t.Fatalf("githubDevicePoll should set session cookie")
	}
}

func TestGitHubDevicePollPendingAndExpired(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	srv.oauth.ClientID = "client-123"
	srv.deviceFlows["pending"] = &deviceFlowEntry{DeviceCode: "device-pending", ExpiresAt: time.Now().Add(time.Minute)}
	srv.deviceFlows["expired"] = &deviceFlowEntry{DeviceCode: "device-expired", ExpiresAt: time.Now().Add(-time.Minute)}

	withAPITestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://github.com/login/oauth/access_token" {
			return apiHTTPResponse(http.StatusOK, `{"error":"authorization_pending"}`), nil
		}
		t.Fatalf("unexpected URL: %s", req.URL.String())
		return nil, nil
	})

	pendingReq := httptest.NewRequest(http.MethodGet, "/auth/github/device/poll?flow_id=pending", nil)
	pendingReq.URL.RawQuery = "flow_id=pending"
	pendingRR := httptest.NewRecorder()
	srv.githubDevicePoll(pendingRR, pendingReq)
	if pendingRR.Code != http.StatusAccepted {
		t.Fatalf("pending status = %d body=%s", pendingRR.Code, pendingRR.Body.String())
	}
	var pendingResp map[string]any
	decodeJSONBody(t, pendingRR, &pendingResp)
	if pendingResp["status"] != "pending" {
		t.Fatalf("pending response mismatch: %+v", pendingResp)
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/auth/github/device/poll?flow_id=expired", nil)
	expiredReq.URL.RawQuery = "flow_id=expired"
	expiredRR := httptest.NewRecorder()
	srv.githubDevicePoll(expiredRR, expiredReq)
	if expiredRR.Code != http.StatusGone {
		t.Fatalf("expired status = %d body=%s", expiredRR.Code, expiredRR.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/auth/github/device/poll", nil)
	missingRR := httptest.NewRecorder()
	srv.githubDevicePoll(missingRR, missingReq)
	if missingRR.Code != http.StatusBadRequest {
		t.Fatalf("missing flow status = %d", missingRR.Code)
	}
}

func TestSavePageLocalAndErrString(t *testing.T) {
	srv, store := newServerWithSpace(t, "local")
	root := filepath.Join(store.load.RootRepoLocalDir, "spaces", "main")
	if _, err := ensureInitialized(root, "main"); err != nil {
		t.Fatalf("ensureInitialized: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/spaces/main/page", strings.NewReader(`{"path":"docs/test","content":"# Hello"}`))
	req = withURLParam(req, "space", "main")
	rr := httptest.NewRecorder()
	srv.savePage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("savePage status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	decodeJSONBody(t, rr, &resp)
	if resp["path"] != "docs/test.md" || resp["save_mode"] != "local" {
		t.Fatalf("savePage response mismatch: %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "test.md"))
	if err != nil {
		t.Fatalf("read saved page: %v", err)
	}
	if string(data) != "# Hello" {
		t.Fatalf("saved page content = %q", string(data))
	}

	if errString(nil) != "" {
		t.Fatalf("errString(nil) should be empty")
	}
	if errString(context.Canceled) != context.Canceled.Error() {
		t.Fatalf("errString(context.Canceled) mismatch")
	}
}

func TestSavePageLocalPreservesTabBytes(t *testing.T) {
	srv, store := newServerWithSpace(t, "local")
	root := filepath.Join(store.load.RootRepoLocalDir, "spaces", "main")
	if _, err := ensureInitialized(root, "main"); err != nil {
		t.Fatalf("ensureInitialized: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/spaces/main/page", strings.NewReader("{\"path\":\"docs/tabs\",\"content\":\"\\talpha\\n\\n beta\"}"))
	req = withURLParam(req, "space", "main")
	rr := httptest.NewRecorder()
	srv.savePage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("savePage status = %d body=%s", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(root, "docs", "tabs.md"))
	if err != nil {
		t.Fatalf("read saved page: %v", err)
	}
	if string(data) != "\talpha\n\n beta" {
		t.Fatalf("saved page content = %q", string(data))
	}
}

func TestPushHelpersPreferSessionToken(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := srv.pushToken(req); got != srv.Cfg.ServerGitToken {
		t.Fatalf("pushToken fallback mismatch: %q", got)
	}
	if got := srv.pushAuthUsername(req); got != "git" {
		t.Fatalf("pushAuthUsername fallback mismatch: %q", got)
	}

	sess := &session.Session{ID: "sid-3", Login: "amit", AccessToken: "token-123"}
	srv.Sessions.Put(sess)
	req.AddCookie(&http.Cookie{Name: "mdwiki_session", Value: sess.ID})
	if got := srv.pushToken(req); got != "token-123" {
		t.Fatalf("pushToken session mismatch: %q", got)
	}
	if got := srv.pushAuthUsername(req); got != "amit" {
		t.Fatalf("pushAuthUsername session mismatch: %q", got)
	}
}

func TestGitHubDeviceStartRequiresClientID(t *testing.T) {
	srv, _ := newServerWithSpace(t, "local")
	req := httptest.NewRequest(http.MethodPost, "/auth/github/device/start", nil)
	rr := httptest.NewRecorder()
	srv.githubDeviceStart(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("githubDeviceStart without client ID status = %d", rr.Code)
	}
}
