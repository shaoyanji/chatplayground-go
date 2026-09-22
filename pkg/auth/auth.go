package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func SaveTokenDirectly(token string) error {
	auth, err := ReadAuth()
	if err != nil {
		auth = &AuthData{}
	}
	auth.Token = token
	auth.SessionCookie = token
	return SaveAuth(auth)
}

// RefreshClerkTokenREST performs a REST-based token renewal attempt via Clerk FAPI
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
	req.Header.Set("Cookie", fmt.Sprintf("__session=%s; __client_uat=%d", cookieVal, auth.UpdatedAt/1000))

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var result struct {
			Jwt string `json:"jwt"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Jwt != "" {
			auth.Token = result.Jwt
			_ = SaveAuth(auth)
			return result.Jwt, nil
		}
	}

	return "", fmt.Errorf("clerk FAPI returned status %d", resp.StatusCode)
}

var refreshMutex sync.Mutex

// RefreshViaBackgroundHelper uses the local stealth runner to renew the token using browser cookies
func RefreshViaBackgroundHelper() (string, error) {
	refreshMutex.Lock()
	defer refreshMutex.Unlock()

	// Check if another goroutine just refreshed the token while waiting for the lock
	if auth, err := ReadAuth(); err == nil && auth.Token != "" {
		if time.Now().UnixMilli()-auth.UpdatedAt < 45*1000 {
			return auth.Token, nil
		}
	}

	helperScript := `
		const { refreshSessionToken } = require('C:/Users/root/.chatplayground/lib/browser.js');
		const { saveAuth } = require('C:/Users/root/.chatplayground/lib/auth.js');
		refreshSessionToken().then(res => {
			if (res && res.token) {
				saveAuth(res);
				process.stdout.write(res.token);
			}
		}).catch(() => {});
	`

	cmd := exec.Command("node", "-e", helperScript)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(out))
	if token != "" {
		return token, nil
	}
	return "", fmt.Errorf("background refresh returned empty token")
}

// GetValidToken returns an active session token, renewing automatically if needed
func GetValidToken() (string, error) {
	if envToken := os.Getenv("CHATPLAYGROUND_TOKEN"); envToken != "" {
		return envToken, nil
	}

	auth, err := ReadAuth()
	if err != nil || auth.Token == "" {
		return "", fmt.Errorf("not authenticated. Please run 'chatplayground-go login' first")
	}

	// If token was refreshed in the last 45 seconds, return immediately (<1ms)
	age := time.Now().UnixMilli() - auth.UpdatedAt
	if age < 45*1000 {
		return auth.Token, nil
	}

	// 1. Try pure REST refresh
	if newToken, err := RefreshClerkTokenREST(auth); err == nil && newToken != "" {
		return newToken, nil
	}

	// 2. Try background stealth refresher
	if newToken, err := RefreshViaBackgroundHelper(); err == nil && newToken != "" {
		return newToken, nil
	}

	// 3. Fallback to existing token
	return auth.Token, nil
}
