package krill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://www.krill-ai.com"
	UserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type apiResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func NewClient() *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	var res struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email":    email,
		"password": password,
	}, &res)
	if err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", errors.New("登录失败: 未返回 token")
	}
	return res.Token, nil
}

func (c *Client) Subscription(ctx context.Context, token string) (SubscriptionPayload, error) {
	var payload SubscriptionPayload
	err := c.do(ctx, http.MethodGet, "/api/subscription", token, nil, &payload)
	return payload, err
}

func (c *Client) do(ctx context.Context, method, path, token string, body any, out any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") {
		return ErrAuthRequired
	}

	var api apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return err
	}
	if !api.Success {
		msg := api.Message
		if msg == "" {
			msg = "API 错误"
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
			strings.Contains(strings.ToLower(msg), "unauthorized") {
			return ErrAuthRequired
		}
		return fmt.Errorf("%s", msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(api.Data, out)
}

var ErrAuthRequired = errors.New("请登录 Krill AI")
