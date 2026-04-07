package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubAccessTokenURL  = "https://github.com/login/oauth/access_token"
	deviceGrantType       = "urn:ietf:params:oauth:grant-type:device_code"
)

// ErrDeviceAuthorizationPending means the user has not finished authorizing yet.
var ErrDeviceAuthorizationPending = errors.New("authorization_pending")

// ErrDeviceSlowDown means GitHub asked the client to poll less frequently.
var ErrDeviceSlowDown = errors.New("slow_down")

// DeviceCodeResponse is returned by GitHub after requesting a device code.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestDeviceCode starts the OAuth 2.0 device authorization grant (GitHub).
func RequestDeviceCode(ctx context.Context, clientID string, scopes []string) (*DeviceCodeResponse, error) {
	if clientID == "" {
		return nil, fmt.Errorf("github device flow: missing client_id")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubDeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github device code: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out DeviceCodeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("github device code: empty device_code or user_code")
	}
	if out.Interval < 1 {
		out.Interval = 5
	}
	return &out, nil
}

type tokenErrBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int    `json:"expires_in"`
}

// ExchangeDeviceCode polls GitHub for an access token after the user authorizes the device.
func ExchangeDeviceCode(ctx context.Context, clientID, deviceCode string) (*oauth2.Token, error) {
	if clientID == "" || deviceCode == "" {
		return nil, fmt.Errorf("github device token: missing client_id or device_code")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", deviceGrantType)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubAccessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tb tokenErrBody
	if err := json.Unmarshal(body, &tb); err != nil {
		return nil, fmt.Errorf("github device token: parse: %w", err)
	}
	switch tb.Error {
	case "":
		if tb.AccessToken == "" {
			return nil, fmt.Errorf("github device token: empty response")
		}
		tok := &oauth2.Token{
			AccessToken: tb.AccessToken,
			TokenType:   tb.TokenType,
		}
		return DeviceTokenWithExpiry(tok, tb.ExpiresIn), nil
	case "authorization_pending":
		return nil, ErrDeviceAuthorizationPending
	case "slow_down":
		return nil, ErrDeviceSlowDown
	case "expired_token":
		return nil, fmt.Errorf("device code expired; start again")
	case "access_denied":
		return nil, fmt.Errorf("access denied")
	default:
		if tb.ErrorDescription != "" {
			return nil, fmt.Errorf("github device token: %s: %s", tb.Error, tb.ErrorDescription)
		}
		return nil, fmt.Errorf("github device token: %s", tb.Error)
	}
}

// DeviceTokenWithExpiry sets Expiry on the token when GitHub includes expires_in (optional).
func DeviceTokenWithExpiry(tok *oauth2.Token, expiresInSec int) *oauth2.Token {
	if tok == nil || expiresInSec <= 0 {
		return tok
	}
	t := *tok
	t.Expiry = time.Now().Add(time.Duration(expiresInSec) * time.Second)
	return &t
}
