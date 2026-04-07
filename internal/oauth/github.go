package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c Config) OAuth2() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       []string{"read:user", "user:email", "repo"},
		Endpoint:     github.Endpoint,
	}
}

// GitHubProfile from GitHub API.
type GitHubProfile struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// FetchGitHubUser returns profile for token.
func FetchGitHubUser(ctx context.Context, tok *oauth2.Token) (*GitHubProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user: %s", resp.Status)
	}
	var u GitHubProfile
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}
