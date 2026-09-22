package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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

type ModelInfo struct {
	BotID        string `json:"botId"`
	ModelName    string `json:"modelName"`
	DisplayName  string `json:"displayName"`
	Provider     string `json:"provider"`
	Endpoint     string `json:"endpoint"`
	SupportImage bool   `json:"supportImage"`
	Group        string `json:"group"`
}

type Client struct {
	HTTPClient *http.Client
	Token      string
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
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

	// Model normalization
	formattedModel := "anthropic/claude-sonnet-5"
	botId := "claude-sonnet-5-l"
	endpoint := "https://app.chatplayground.ai/api/chat/azure"

	cleanModel := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(cleanModel, "deepseek") || cleanModel == "r1":
		botId = "deepseek-r1"
		formattedModel = "deepseek/deepseek-r1-0528"
		endpoint = "https://app.chatplayground.ai/api/chat/azure"
	case strings.Contains(cleanModel, "gpt-5") || cleanModel == "gpt" || cleanModel == "sol":
		botId = "gpt-5.6-sol"
		formattedModel = "openai/gpt-5.6-sol"
		endpoint = "https://app.chatplayground.ai/api/chat/azure"
	case strings.Contains(cleanModel, "gemini") || cleanModel == "flash":
		botId = "gemini-3.8-flash-l"
		formattedModel = "google/gemini-3.8-flash"
		endpoint = "https://app.chatplayground.ai/api/chat/azure"
	case strings.Contains(cleanModel, "grok"):
		botId = "grok-4.6"
		formattedModel = "xai/grok-4.6"
		endpoint = "https://app.chatplayground.ai/api/chat/azure"
	default:
		botId = "claude-sonnet-5-l"
		formattedModel = "anthropic/claude-sonnet-5"
		endpoint = "https://app.chatplayground.ai/api/chat/azure"
	}

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

	payload := map[string]interface{}{
		"messages": []Message{
			{Role: "user", Content: userContent},
		},
		"model":          formattedModel,
		"botId":          botId,
		"chatId":         "",
		"isRegenerate":   false,
		"promptTemplate": nil,
		"fileUrl":        nil,
		"submissionId":   nil,
		"noSave":         false,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
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
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return "", fmt.Errorf("authentication expired (401)")
		}
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyErr))
	}

	chatIdRegex := regexp.MustCompile(`CHAT_ID:[a-zA-Z0-9_-]{15,50}\n?`)
	chatIdEndRegex := regexp.MustCompile(`CHAT_ID:[a-zA-Z0-9_-]*$`)

	buf := make([]byte, 4096)
	var fullText strings.Builder

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
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
			return fullText.String(), err
		}
	}

	return fullText.String(), nil
}

func (c *Client) GenerateImage(prompt string, size string, outputPath string) (string, error) {
	token := c.Token
	if token == "" {
		var err error
		token, err = auth.GetValidToken()
		if err != nil {
			return "", err
		}
	}

	if size == "" {
		size = "1024x1024"
	}

	payload := map[string]interface{}{
		"prompt": prompt,
		"apiKey": "",
		"model":  "gpt-image-2",
		"size":   size,
	}

	payloadBytes, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://app.chatplayground.ai/api/generate-image/gpt-image-2", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Cookie", fmt.Sprintf("__session=%s", token))
	req.Header.Set("Origin", "https://web.chatplayground.ai")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image generation failed with status %d", resp.StatusCode)
	}

	var res struct {
		Url string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if outputPath != "" && res.Url != "" {
		imgResp, err := http.Get(res.Url)
		if err == nil {
			defer imgResp.Body.Close()
			imgData, _ := io.ReadAll(imgResp.Body)
			_ = os.WriteFile(outputPath, imgData, 0644)
		}
	}

	return res.Url, nil
}
