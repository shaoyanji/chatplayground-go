package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type AuthData struct {
	Token         string                 `json:"token"`
	SessionID     string                 `json:"sessionId"`
	SessionCookie string                 `json:"sessionCookie"`
	User          map[string]interface{} `json:"user"`
	UpdatedAt     int64                  `json:"updatedAt"`
}

func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chatplayground")
}

func GetAuthFilePath() string {
	return filepath.Join(GetConfigDir(), "auth.json")
}

func ReadAuth() (*AuthData, error) {
	path := GetAuthFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var auth AuthData
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

func SaveAuth(auth *AuthData) error {
	dir := GetConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	auth.UpdatedAt = time.Now().UnixMilli()
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(GetAuthFilePath(), data, 0600)
}

// RefreshClerkTokenREST performs a pure-REST browserless token renewal via Clerk's FAPI
func RefreshClerkTokenREST(auth *AuthData) (string, error) {
	if auth.SessionID == "" {
		return "", fmt.Errorf("no session ID found for token refresh")
	}

	url := fmt.Sprintf("https://clerk.chatplayground.ai/v1/client/sessions/%s/tokens", auth.SessionID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://web.chatplayground.ai")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	cookieVal := auth.SessionCookie
	if cookieVal == "" {
		cookieVal = auth.Token
	}
	req.Header.Set("Cookie", fmt.Sprintf("__session=%s", cookieVal))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clerk refresh returned status %d", resp.StatusCode)
	}

	var result struct {
		Jwt string `json:"jwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Jwt != "" {
		auth.Token = result.Jwt
		_ = SaveAuth(auth)
		return result.Jwt, nil
	}

	return "", fmt.Errorf("empty jwt received from clerk")
}

// GetValidToken returns an active session token, refreshing RESTfully if needed
func GetValidToken() (string, error) {
	if envToken := os.Getenv("CHATPLAYGROUND_TOKEN"); envToken != "" {
		return envToken, nil
	}

	auth, err := ReadAuth()
	if err != nil || auth.Token == "" {
		return "", fmt.Errorf("not authenticated. Please run 'chatplayground login' first")
	}

	// If token was refreshed in the last 45 seconds, it's fresh
	age := time.Now().UnixMilli() - auth.UpdatedAt
	if age < 45*1000 {
		return auth.Token, nil
	}

	// Try pure REST refresh
	if newToken, err := RefreshClerkTokenREST(auth); err == nil && newToken != "" {
		return newToken, nil
	}

	// Fallback to existing token
	return auth.Token, nil
}
