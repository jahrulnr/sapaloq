package credentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/debug"
)

const (
	cursorOAuthClientID = "KbZUR41cY7W6zRSdpSUJ7I7mLYBKOCmB"
	cursorOAuthTokenURL = "https://api2.cursor.sh/oauth/token"
	tokenRefreshSkew    = 5 * time.Minute
)

// EnsureFresh refreshes the access token when it is missing, expired, or near expiry.
func EnsureFresh(ctx context.Context, creds *Credentials) error {
	if creds == nil {
		return fmt.Errorf("credentials missing")
	}
	if creds.AccessToken != "" && !tokenNeedsRefresh(creds.AccessToken) {
		return nil
	}
	if strings.TrimSpace(creds.RefreshToken) == "" {
		if creds.AccessToken != "" {
			return nil
		}
		return fmt.Errorf("cursor refresh token missing")
	}
	fresh, err := RefreshAccessToken(ctx, creds.RefreshToken)
	if err != nil {
		return err
	}
	if fresh.ShouldLogout || fresh.AccessToken == "" {
		return fmt.Errorf("cursor session expired; re-login in Cursor IDE")
	}
	creds.AccessToken = fresh.AccessToken
	if fresh.RefreshToken != "" {
		creds.RefreshToken = fresh.RefreshToken
	}
	creds.Source = creds.Source + "+refresh"
	debug.Debugf("credentials: refreshed access token via oauth")
	return nil
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ShouldLogout bool   `json:"shouldLogout"`
}

// RefreshAccessToken exchanges a refresh token for a new access token.
func RefreshAccessToken(ctx context.Context, refreshToken string) (refreshResponse, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     cursorOAuthClientID,
		"refresh_token": strings.TrimSpace(refreshToken),
	})
	if err != nil {
		return refreshResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cursorOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return refreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return refreshResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return refreshResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return refreshResponse{}, fmt.Errorf("cursor oauth refresh http %d", resp.StatusCode)
	}
	var out refreshResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return refreshResponse{}, err
	}
	return out, nil
}

func tokenNeedsRefresh(token string) bool {
	exp, ok := jwtExp(token)
	if !ok {
		return false
	}
	return time.Now().UTC().Add(tokenRefreshSkew).After(exp)
}

func jwtExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}
