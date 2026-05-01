package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type UsageLimit struct {
	Utilization float64    `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

type UsageData struct {
	FiveHour          UsageLimit  `json:"five_hour"`
	SevenDay          UsageLimit  `json:"seven_day"`
	SevenDaySonnet    *UsageLimit `json:"seven_day_sonnet"`
	SevenDayOpus      *UsageLimit `json:"seven_day_opus"`
	SevenDayOauthApps *UsageLimit `json:"seven_day_oauth_apps"`
	SevenDayCowork    *UsageLimit `json:"seven_day_cowork"`
	IguanaNecktie     *UsageLimit `json:"iguana_necktie"`
	ExtraUsage        *UsageLimit `json:"extra_usage"`
}

type Client struct {
	httpClient *http.Client
	mu         sync.Mutex
	sessionKey string
	orgID      string
}

func NewClient(sessionKey, orgID string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		sessionKey: sessionKey,
		orgID:      orgID,
	}
}

func (c *Client) SessionKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionKey
}

func (c *Client) setSessionKey(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionKey = s
}

func (c *Client) SetCredentials(sessionKey, orgID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionKey = sessionKey
	c.orgID = orgID
}

func (c *Client) FetchUsage(ctx context.Context) (*UsageData, string, error) {
	url := fmt.Sprintf("https://claude.ai/api/organizations/%s/usage", c.orgID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-client-platform", "web_claude_ai")
	req.Header.Set("user-agent", userAgent)
	req.Header.Set("origin", "https://claude.ai")
	req.Header.Set("referer", "https://claude.ai/settings/usage")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: c.SessionKey()})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("auth response %d body: %s", resp.StatusCode, string(body))
		return nil, "", fmt.Errorf("auth error %d — see debug.log", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("http %d body: %s", resp.StatusCode, string(body))
		return nil, "", fmt.Errorf("http %d — see debug.log", resp.StatusCode)
	}

	var data UsageData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}

	var newKey string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "sessionKey" && cookie.Value != "" {
			newKey = cookie.Value
		}
	}
	if newKey != "" && newKey != c.SessionKey() {
		c.setSessionKey(newKey)
	}

	return &data, newKey, nil
}
