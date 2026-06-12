package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client hits api.themoviedb.org/3 on behalf of the prefetch CLI. v3 keys
// (api_key=... query param) and v4 read tokens (Bearer header) are both
// accepted — the client picks the right auth based on token shape.
type Client struct {
	apiKey  string
	http    *http.Client
	baseURL string
	imgBase string
}

const (
	defaultBaseURL = "https://api.themoviedb.org/3"
	defaultImgBase = "https://image.tmdb.org/t/p/w300"
)

// NewClient constructs a Client. apiKey is your TMDB v3 API key OR a v4
// Bearer read access token. v4 tokens are JWTs (start with "eyJ"); v3 keys
// are 32-char hex.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: defaultBaseURL,
		imgBase: defaultImgBase,
	}
}

// isBearer reports whether the configured key is a v4 read token (JWT).
func (c *Client) isBearer() bool { return strings.HasPrefix(c.apiKey, "eyJ") }

// get builds and executes a GET. v3 keys ride as ?api_key=, v4 tokens go
// in Authorization: Bearer.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if q == nil {
		q = url.Values{}
	}
	if !c.isBearer() {
		q.Set("api_key", c.apiKey)
	}
	full := c.baseURL + path
	if encoded := q.Encode(); encoded != "" {
		full += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.isBearer() {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb GET %s: %s — %s", path, resp.Status, truncate(string(body), 200))
	}
	return body, nil
}

// FetchShow returns the top-level show record.
func (c *Client) FetchShow(ctx context.Context, showID int) (*Show, error) {
	body, err := c.get(ctx, fmt.Sprintf("/tv/%d", showID), nil)
	if err != nil {
		return nil, err
	}
	var s Show
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse show: %w", err)
	}
	return &s, nil
}

// FetchSeason returns a season including its full episode list. Single
// API call — TMDB embeds episodes in the season response.
func (c *Client) FetchSeason(ctx context.Context, showID, seasonNumber int) (*Season, error) {
	body, err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", showID, seasonNumber), nil)
	if err != nil {
		return nil, err
	}
	var s Season
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse season %d: %w", seasonNumber, err)
	}
	return &s, nil
}

// FetchImage downloads a TMDB-hosted image (still, poster, backdrop) and
// returns the bytes. Caller writes to disk.
func (c *Client) FetchImage(ctx context.Context, imagePath string) ([]byte, error) {
	full := c.imgBase + imagePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image GET %s: %w", imagePath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image %s: %s", imagePath, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("image %s read: %w", imagePath, err)
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
