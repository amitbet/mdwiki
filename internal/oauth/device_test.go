package oauth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withTestHTTPClient(t *testing.T, fn roundTripFunc) {
	t.Helper()
	prev := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fn}
	t.Cleanup(func() {
		http.DefaultClient = prev
	})
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRequestDeviceCode(t *testing.T) {
	if _, err := RequestDeviceCode(context.Background(), "", nil); err == nil {
		t.Fatalf("RequestDeviceCode should reject missing client ID")
	}

	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.String() != githubDeviceCodeURL {
			t.Fatalf("url = %s, want %s", req.URL.String(), githubDeviceCodeURL)
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept header = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Contains(body, []byte("client_id=client-123")) {
			t.Fatalf("request body missing client_id: %q", string(body))
		}
		if !bytes.Contains(body, []byte("scope=repo+read%3Auser")) {
			t.Fatalf("request body missing scopes: %q", string(body))
		}
		return httpResponse(http.StatusOK, `{"device_code":"dev","user_code":"user","verification_uri":"https://github.com/login/device","interval":0}`), nil
	})

	resp, err := RequestDeviceCode(context.Background(), "client-123", []string{"repo", "read:user"})
	if err != nil {
		t.Fatalf("RequestDeviceCode unexpected error: %v", err)
	}
	if resp.DeviceCode != "dev" || resp.UserCode != "user" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Interval != 5 {
		t.Fatalf("Interval = %d, want fallback 5", resp.Interval)
	}
}

func TestRequestDeviceCodeErrors(t *testing.T) {
	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusBadRequest, `{"error":"bad_verification_code"}`), nil
	})

	if _, err := RequestDeviceCode(context.Background(), "client-123", nil); err == nil || !strings.Contains(err.Error(), "Bad Request") {
		t.Fatalf("expected HTTP error, got %v", err)
	}

	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, `{"device_code":"","user_code":""}`), nil
	})
	if _, err := RequestDeviceCode(context.Background(), "client-123", nil); err == nil || !strings.Contains(err.Error(), "empty device_code or user_code") {
		t.Fatalf("expected empty-code error, got %v", err)
	}
}

func TestExchangeDeviceCode(t *testing.T) {
	if _, err := ExchangeDeviceCode(context.Background(), "", "device"); err == nil {
		t.Fatalf("ExchangeDeviceCode should reject missing client ID")
	}

	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Contains(body, []byte("device_code=device-123")) {
			t.Fatalf("request body missing device code: %q", string(body))
		}
		return httpResponse(http.StatusOK, `{"access_token":"token-123","token_type":"bearer","expires_in":60}`), nil
	})

	before := time.Now()
	tok, err := ExchangeDeviceCode(context.Background(), "client-123", "device-123")
	if err != nil {
		t.Fatalf("ExchangeDeviceCode unexpected error: %v", err)
	}
	if tok.AccessToken != "token-123" || tok.TokenType != "bearer" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if tok.Expiry.Before(before.Add(55*time.Second)) || tok.Expiry.After(before.Add(65*time.Second)) {
		t.Fatalf("unexpected expiry: %v", tok.Expiry)
	}
}

func TestExchangeDeviceCodeErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
		wantMsg string
	}{
		{name: "pending", body: `{"error":"authorization_pending"}`, wantErr: ErrDeviceAuthorizationPending},
		{name: "slow_down", body: `{"error":"slow_down"}`, wantErr: ErrDeviceSlowDown},
		{name: "denied", body: `{"error":"access_denied"}`, wantMsg: "access denied"},
		{name: "described", body: `{"error":"incorrect_client_credentials","error_description":"bad client"}`, wantMsg: "bad client"},
		{name: "empty", body: `{"access_token":"","token_type":"bearer"}`, wantMsg: "empty response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				return httpResponse(http.StatusOK, tt.body), nil
			})

			_, err := ExchangeDeviceCode(context.Background(), "client-123", "device-123")
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("error = %v, want message containing %q", err, tt.wantMsg)
			}
		})
	}
}

func TestDeviceTokenWithExpiryAndFetchGitHubUser(t *testing.T) {
	base := &oauth2.Token{AccessToken: "token-123", TokenType: "bearer"}
	if got := DeviceTokenWithExpiry(base, 0); got != base {
		t.Fatalf("zero expiry should return original token pointer")
	}

	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.github.com/user" {
			t.Fatalf("url = %s", req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("authorization header = %q", got)
		}
		return httpResponse(http.StatusOK, `{"id":42,"login":"amit","name":"Amit","avatar_url":"https://example.com/avatar.png"}`), nil
	})

	profile, err := FetchGitHubUser(context.Background(), base)
	if err != nil {
		t.Fatalf("FetchGitHubUser unexpected error: %v", err)
	}
	if profile.Login != "amit" || profile.ID != 42 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestFetchGitHubUserStatusErrorAndOAuth2Config(t *testing.T) {
	cfg := Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "http://localhost/callback",
	}
	oauthCfg := cfg.OAuth2()
	if oauthCfg.ClientID != cfg.ClientID || oauthCfg.RedirectURL != cfg.RedirectURL {
		t.Fatalf("unexpected oauth config: %+v", oauthCfg)
	}
	if len(oauthCfg.Scopes) != 3 {
		t.Fatalf("unexpected scopes: %+v", oauthCfg.Scopes)
	}

	withTestHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusUnauthorized, `{"message":"bad credentials"}`), nil
	})
	if _, err := FetchGitHubUser(context.Background(), &oauth2.Token{AccessToken: "bad"}); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}
