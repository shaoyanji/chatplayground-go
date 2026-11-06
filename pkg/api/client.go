package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shaoyanji/chatplayground-go/pkg/auth"
)

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ImageUrlContent struct {
	Type     string   `json:"type"`
	ImageUrl ImageUrl `json:"image_url"`
}

type ImageUrl struct {
	Url    string `json:"url"`
	Detail string `json:"detail"`
}

// ModelInfo is the catalog entry, not just what the table prints. The
// entitlement fields are the point: a `premiumOnly` id answers 403 "only
// available to active subscribers", so a caller that prints the catalog without
// them is advertising models it cannot use.
type ModelInfo struct {
	BotID        string `json:"botId"`
	ModelName    string `json:"modelName"`
	DisplayName  string `json:"displayName"`
	Provider     string `json:"provider"`
	Endpoint     string `json:"endpoint"`
	SupportImage bool   `json:"supportImage"`
	Group        string `json:"group"`
	Active       bool   `json:"active"`
	PremiumOnly  bool   `json:"premiumOnly"`
	LifetimeOnly bool   `json:"lifetimeOnly"`
	Tier         string `json:"tier"`
	CreditWeight int    `json:"creditWeight"`
	SortIndex    int    `json:"sortIndex"`
}

type Client struct {
	HTTPClient *http.Client
	Token      string
	ChatID     string
	Messages   []Message
}

func NewClient() *Client {
	// HTTP/1.1 on purpose. With the default transport (HTTP/2) the endpoint
	// answers a follow-up turn on an existing chatId with 400 "Send only the new
	// prompt. Conversation history is not accepted." — measured 3/3 failures
	// with HTTP/2 and 3/3 successes with ForceAttemptHTTP2 off, same payload,
	// same token. Keep the negotiation off here rather than special-casing 400.
	transport := &http.Transport{
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 1,
	}
	return &Client{
		HTTPClient: &http.Client{Timeout: 120 * time.Second, Transport: transport},
	}
}

func (c *Client) ResetSession() {
	c.ChatID = ""
	c.Messages = nil
}

func (c *Client) GetModels() ([]ModelInfo, error) {
	req, err := http.NewRequest("GET", "https://app.chatplayground.ai/api/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var models []ModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

func (c *Client) UploadImage(filePathOrUrl string) (string, error) {
	if strings.HasPrefix(filePathOrUrl, "http://") || strings.HasPrefix(filePathOrUrl, "https://") {
		return filePathOrUrl, nil
	}

	fileData, err := os.ReadFile(filePathOrUrl)
	if err != nil {
		return "", fmt.Errorf("failed to read image file: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePathOrUrl))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileData); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://temp-file-host.chatplayground.ai/upload", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Url  string `json:"url"`
		Data struct {
			Url string `json:"url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	finalUrl := res.Url
	if finalUrl == "" {
		finalUrl = res.Data.Url
	}
	if finalUrl == "" {
		return "", fmt.Errorf("image upload did not return URL")
	}

	finalUrl = strings.ReplaceAll(finalUrl, "https://tmpfiles.org/", "https://tmpfiles.org/dl/")
	return finalUrl, nil
}

func (c *Client) StreamQuery(model string, prompt string, imagePath string, onChunk func(string)) (string, error) {
	token := c.Token
	if token == "" {
		var err error
		token, err = auth.GetValidToken()
		if err != nil {
			return "", err
		}
	}

	// One resolver, shared with Chat(). The hand-written switch this replaced
	// had a botId per family and had gone stale — `-m gpt-6-luna` resolved to
	// gpt-5.6-sol and the endpoint answered "Model not supported". The catalog
	// is authoritative, so ask it.
	target := ResolveTarget(context.Background(), model)
	formattedModel := target.Model
	botId := target.BotID
	endpoint := target.Endpoint

	var userContent interface{} = prompt
	if imagePath != "" {
		imgUrl, err := c.UploadImage(imagePath)
		if err == nil && imgUrl != "" {
			userContent = []interface{}{
				TextContent{Type: "text", Text: prompt},
				ImageUrlContent{Type: "image_url", ImageUrl: ImageUrl{Url: imgUrl, Detail: "auto"}},
			}
		}
	}

	// Add user message to history
	c.Messages = append(c.Messages, Message{Role: "user", Content: userContent})
	// The endpoint keeps the thread server-side, keyed by chatId, and rejects a
	// resent transcript ("Send only the new prompt"). Send the new prompt only;
	// c.Messages stays the local record of the conversation.
	sendMessages := c.Messages[len(c.Messages)-1:]

	payload := map[string]interface{}{
		"messages":       sendMessages,
		"model":          formattedModel,
		"botId":          botId,
		"chatId":         c.ChatID,
		"isRegenerate":   false,
		"promptTemplate": nil,
		"fileUrl":        nil,
		"submissionId":   nil,
		"noSave":         false,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		if len(c.Messages) > 0 {
			c.Messages = c.Messages[:len(c.Messages)-1]
		}
		return "", err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		if len(c.Messages) > 0 {
			c.Messages = c.Messages[:len(c.Messages)-1]
		}
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Cookie", fmt.Sprintf("__session=%s", token))
	req.Header.Set("Origin", "https://web.chatplayground.ai")
	req.Header.Set("Referer", "https://web.chatplayground.ai/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if len(c.Messages) > 0 {
			c.Messages = c.Messages[:len(c.Messages)-1]
		}
		return "", err
	}
	defer resp.Body.Close()

	// Auto-retry once on 401 Unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		// REST mint only: RefreshViaBackgroundHelper shells out to a node script
		// with a hardcoded Windows path, which cannot work on a headless host and
		// turns a clear 401 into a mystery. The `__client` cookie in auth.json is
		// what makes an unattended refresh possible at all.
		var newToken string
		if stored, aerr := auth.ReadAuth(); aerr == nil {
			newToken, err = auth.RefreshClerkTokenREST(stored)
		}
		if err == nil && newToken != "" {
			c.Token = newToken
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", newToken))
			req.Header.Set("Cookie", fmt.Sprintf("__session=%s", newToken))
			req.Body = io.NopCloser(bytes.NewBuffer(payloadBytes))
			resp.Body.Close()
			resp, err = c.HTTPClient.Do(req)
			if err != nil {
				if len(c.Messages) > 0 {
					c.Messages = c.Messages[:len(c.Messages)-1]
				}
				return "", err
			}
			defer resp.Body.Close()
		}
	}

	if resp.StatusCode != http.StatusOK {
		if len(c.Messages) > 0 {
			c.Messages = c.Messages[:len(c.Messages)-1]
		}
		bodyErr, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return "", fmt.Errorf("authentication expired (401). Please run 'chatplayground-go login'")
		}
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyErr))
	}

	chatIdExtractRegex := regexp.MustCompile(`CHAT_ID:([a-zA-Z0-9_-]{15,50})`)
	chatIdRegex := regexp.MustCompile(`CHAT_ID:[a-zA-Z0-9_-]{15,50}\n?`)
	chatIdEndRegex := regexp.MustCompile(`CHAT_ID:[a-zA-Z0-9_-]*$`)

	buf := make([]byte, 4096)
	var fullText strings.Builder

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			if match := chatIdExtractRegex.FindStringSubmatch(chunk); len(match) > 1 {
				c.ChatID = match[1]
			}
			cleanChunk := chatIdRegex.ReplaceAllString(chunk, "")
			cleanChunk = chatIdEndRegex.ReplaceAllString(cleanChunk, "")
			if cleanChunk != "" {
				fullText.WriteString(cleanChunk)
				if onChunk != nil {
					onChunk(cleanChunk)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			if len(c.Messages) > 0 {
				c.Messages = c.Messages[:len(c.Messages)-1]
			}
			return fullText.String(), err
		}
	}

	// Save assistant response to conversation history for multi-turn context
	c.Messages = append(c.Messages, Message{Role: "assistant", Content: fullText.String()})

	return fullText.String(), nil
}

// GetUsage answers what the plan allows, which the catalog cannot: its
// `premiumOnly` flag is global, while this is per account. A lifetime pool
// reports `key: lifetime-pool`, `name: unlimited` and its own rate limits.
func (c *Client) GetUsage() (map[string]any, error) {
	token := c.Token
	if token == "" {
		t, err := auth.GetValidToken()
		if err != nil {
			return nil, err
		}
		token = t
	}

	req, err := http.NewRequest("GET", "https://app.chatplayground.ai/api/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "__session="+token)
	req.Header.Set("Origin", "https://web.chatplayground.ai")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("usage failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// ImageResult is the generation endpoint's answer, plus the model that produced
// it and the local file when one was requested.
type ImageResult struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Size   string `json:"size"`
	Path   string `json:"path,omitempty"`
}

// PickImageModel resolves the generator from the live catalog. The endpoint path
// carries the model id, and ids are retired without notice: the hardcoded
// `gpt-image-2` answered 400 "The model is not supported" on 2026-11-06, while
// the catalog offered gpt-image-2.5-sunburst and -flare.
func (c *Client) PickImageModel(preferred string) string {
	if preferred != "" {
		return preferred
	}
	if models, err := c.GetModels(); err == nil {
		for _, m := range models {
			if m.Group == "image" && m.Active && !m.PremiumOnly {
				return m.BotID
			}
		}
	}
	return "gpt-image-2.5-sunburst"
}

func (c *Client) GenerateImage(prompt string, model string, size string, outputPath string) (ImageResult, error) {
	res := ImageResult{Prompt: prompt, Model: c.PickImageModel(model), Size: size}
	if res.Size == "" {
		res.Size = "1024x1024"
	}

	attempt := func() (*http.Response, error) {
		token := c.Token
		if token == "" {
			t, err := auth.GetValidToken()
			if err != nil {
				return nil, err
			}
			token = t
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"prompt": prompt,
			"apiKey": "",
			"model":  res.Model,
			"size":   res.Size,
		})
		endpoint := "https://app.chatplayground.ai/api/generate-image/" + url.PathEscape(res.Model)
		req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "__session="+token)
		req.Header.Set("Origin", "https://web.chatplayground.ai")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		return c.HTTPClient.Do(req)
	}

	resp, err := attempt()
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()

	// 401 is a stale 60-second token, not a lost session: mint and retry once.
	// The retired node helper is never the answer here — it needs a Windows
	// browser install and silently returns nothing everywhere else.
	if resp.StatusCode == http.StatusUnauthorized {
		if data, rerr := auth.ReadAuth(); rerr == nil {
			if t, rerr := auth.RefreshClerkTokenREST(data); rerr == nil && t != "" {
				c.Token = t
				resp.Body.Close()
				resp, err = attempt()
				if err != nil {
					return res, err
				}
				defer resp.Body.Close()
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		// The endpoint names its own failure ("The model is not supported");
		// dropping the body turned that into a bare code.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return res, fmt.Errorf("image generation failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var body struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return res, err
	}
	res.ID, res.URL = body.ID, body.URL

	if outputPath != "" && res.URL != "" {
		imgResp, err := http.Get(res.URL)
		if err != nil {
			return res, fmt.Errorf("generated at %s but could not download it: %w", res.URL, err)
		}
		defer imgResp.Body.Close()
		data, _ := io.ReadAll(imgResp.Body)
		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			return res, err
		}
		res.Path = outputPath
	}
	return res, nil
}
