package sub2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"quotaball/internal/krill"
)

const (
	apiPrefix = "/api/v1"
	UserAgent = krill.UserAgent
)

var ErrAuthRequired = errors.New("请登录 Sub2")

var errUnsupportedTurnstile = errors.New("该 Sub2 站点启用了 Turnstile 验证码，当前版本暂不支持")

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type AuthResponse struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	ExpiresIn       int64  `json:"expires_in"`
	TokenType       string `json:"token_type"`
	Requires2FA     bool   `json:"requires_2fa"`
	TempToken       string `json:"temp_token"`
	UserEmailMasked string `json:"user_email_masked"`
	User            User   `json:"user"`
}

type User struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	Balance     float64 `json:"balance"`
	Concurrency int     `json:"concurrency"`
	Status      string  `json:"status"`
}

type SubscriptionSummary struct {
	ActiveCount   int                       `json:"active_count"`
	TotalUsedUSD  float64                   `json:"total_used_usd"`
	Subscriptions []SubscriptionSummaryItem `json:"subscriptions"`
}

type SubscriptionSummaryItem struct {
	ID              int64   `json:"id"`
	GroupID         int64   `json:"group_id"`
	GroupName       string  `json:"group_name"`
	Status          string  `json:"status"`
	DailyUsedUSD    float64 `json:"daily_used_usd"`
	DailyLimitUSD   float64 `json:"daily_limit_usd"`
	WeeklyUsedUSD   float64 `json:"weekly_used_usd"`
	WeeklyLimitUSD  float64 `json:"weekly_limit_usd"`
	MonthlyUsedUSD  float64 `json:"monthly_used_usd"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"`
	ExpiresAt       string  `json:"expires_at"`
}

type SubscriptionProgressInfo struct {
	Subscription *UserSubscription     `json:"subscription"`
	Progress     *SubscriptionProgress `json:"progress"`
}

type UserSubscription struct {
	ID                 int64   `json:"id"`
	UserID             int64   `json:"user_id"`
	GroupID            int64   `json:"group_id"`
	Status             string  `json:"status"`
	StartsAt           string  `json:"starts_at"`
	DailyUsageUSD      float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD     float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD    float64 `json:"monthly_usage_usd"`
	DailyWindowStart   string  `json:"daily_window_start"`
	WeeklyWindowStart  string  `json:"weekly_window_start"`
	MonthlyWindowStart string  `json:"monthly_window_start"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	ExpiresAt          string  `json:"expires_at"`
	Group              *Group  `json:"group"`
}

type Group struct {
	ID              int64         `json:"id"`
	Name            string        `json:"name"`
	Platform        string        `json:"platform"`
	Subscription    string        `json:"subscription_type"`
	DailyLimitUSD   nullableFloat `json:"daily_limit_usd"`
	WeeklyLimitUSD  nullableFloat `json:"weekly_limit_usd"`
	MonthlyLimitUSD nullableFloat `json:"monthly_limit_usd"`
}

type SubscriptionProgress struct {
	SubscriptionID int64        `json:"subscription_id"`
	Daily          *QuotaWindow `json:"daily"`
	Weekly         *QuotaWindow `json:"weekly"`
	Monthly        *QuotaWindow `json:"monthly"`
	ExpiresAt      string       `json:"expires_at"`
	DaysRemaining  nullableInt  `json:"days_remaining"`
}

type QuotaWindow struct {
	Used           float64       `json:"used_usd"`
	Limit          nullableFloat `json:"limit_usd"`
	Remaining      nullableFloat `json:"remaining_usd"`
	Percentage     float64       `json:"percentage"`
	ResetInSeconds nullableInt   `json:"resets_in_seconds"`
	usedSet        bool
}

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type apiResponse[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type nullableFloat struct {
	Valid bool
	Value float64
}

type nullableInt struct {
	Valid bool
	Value int
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	} else {
		copyClient := *httpClient
		httpClient = &copyClient
	}
	return &Client{BaseURL: base, HTTPClient: httpClient}, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请输入 Sub2 网站地址")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("Sub2 网站地址无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Sub2 网站地址必须以 http:// 或 https:// 开头")
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", errors.New("Sub2 网站地址必须使用 https://")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(u.Path, apiPrefix) {
		u.Path = strings.TrimSuffix(u.Path, apiPrefix)
	}
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func (c *Client) Login(ctx context.Context, email, password string) (AuthResponse, error) {
	var out AuthResponse
	err := c.do(ctx, http.MethodPost, "/auth/login", "", map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	}, &out)
	if err != nil {
		if isTurnstileMessage(err.Error()) {
			return AuthResponse{}, errUnsupportedTurnstile
		}
		return AuthResponse{}, err
	}
	if out.Requires2FA {
		return AuthResponse{}, errors.New("该 Sub2 账号启用了 2FA，当前版本暂不支持")
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return AuthResponse{}, errors.New("Sub2 登录未返回 access token")
	}
	return out, nil
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (RefreshResponse, error) {
	var out RefreshResponse
	err := c.do(ctx, http.MethodPost, "/auth/refresh", "", map[string]string{
		"refresh_token": strings.TrimSpace(refreshToken),
	}, &out)
	if err != nil {
		return RefreshResponse{}, err
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return RefreshResponse{}, ErrAuthRequired
	}
	return out, nil
}

func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return nil
	}
	return c.do(ctx, http.MethodPost, "/auth/logout", "", map[string]string{
		"refresh_token": strings.TrimSpace(refreshToken),
	}, nil)
}

func (c *Client) Profile(ctx context.Context, token string) (User, error) {
	var user User
	err := c.do(ctx, http.MethodGet, "/auth/me", token, nil, &user)
	if err == nil {
		return user, nil
	}
	if errors.Is(err, ErrAuthRequired) {
		return User{}, err
	}
	err = c.do(ctx, http.MethodGet, "/user/profile", token, nil, &user)
	return user, err
}

func (c *Client) SubscriptionSummary(ctx context.Context, token string) (SubscriptionSummary, error) {
	var out SubscriptionSummary
	err := c.do(ctx, http.MethodGet, "/subscriptions/summary", token, nil, &out)
	return out, err
}

func (c *Client) SubscriptionProgress(ctx context.Context, token string) ([]SubscriptionProgressInfo, error) {
	var out []SubscriptionProgressInfo
	err := c.do(ctx, http.MethodGet, "/subscriptions/progress", token, nil, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path, token string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+apiPrefix+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
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
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var api apiResponse[json.RawMessage]
	if err := json.Unmarshal(respBody, &api); err != nil {
		if isTurnstileMessage(string(respBody)) {
			return errUnsupportedTurnstile
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return ErrAuthRequired
		}
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if isTurnstileMessage(api.Message) {
			return errUnsupportedTurnstile
		}
		return ErrAuthRequired
	}
	if api.Code != 0 {
		msg := strings.TrimSpace(api.Message)
		if msg == "" {
			msg = "Sub2 请求失败"
		}
		if isTurnstileMessage(msg) {
			return errUnsupportedTurnstile
		}
		if isAuthMessage(msg) {
			return ErrAuthRequired
		}
		return fmt.Errorf("%s", msg)
	}
	if out == nil {
		return nil
	}
	if len(api.Data) == 0 || bytes.Equal(bytes.TrimSpace(api.Data), []byte("null")) {
		return nil
	}
	return json.Unmarshal(api.Data, out)
}

func (f *nullableFloat) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*f = nullableFloat{}
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		raw = strings.TrimSpace(s)
		if raw == "" {
			*f = nullableFloat{}
			return nil
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return err
	}
	*f = nullableFloat{Valid: true, Value: value}
	return nil
}

func (w *QuotaWindow) UnmarshalJSON(data []byte) error {
	var raw struct {
		Used                 nullableFloat `json:"used_usd"`
		LegacyUsed           nullableFloat `json:"used"`
		Limit                nullableFloat `json:"limit_usd"`
		LegacyLimit          nullableFloat `json:"limit"`
		Remaining            nullableFloat `json:"remaining_usd"`
		LegacyRemaining      nullableFloat `json:"remaining"`
		Percentage           float64       `json:"percentage"`
		ResetInSeconds       nullableInt   `json:"resets_in_seconds"`
		LegacyResetInSeconds nullableInt   `json:"reset_in_seconds"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*w = QuotaWindow{
		Limit:          firstValidFloat(raw.Limit, raw.LegacyLimit),
		Remaining:      firstValidFloat(raw.Remaining, raw.LegacyRemaining),
		Percentage:     raw.Percentage,
		ResetInSeconds: firstValidInt(raw.ResetInSeconds, raw.LegacyResetInSeconds),
	}
	if raw.Used.Valid {
		w.Used = raw.Used.Value
		w.usedSet = true
	} else if raw.LegacyUsed.Valid {
		w.Used = raw.LegacyUsed.Value
		w.usedSet = true
	}
	return nil
}

func (i *nullableInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*i = nullableInt{}
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		raw = strings.TrimSpace(s)
		if raw == "" {
			*i = nullableInt{}
			return nil
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	*i = nullableInt{Valid: true, Value: value}
	return nil
}

func firstValidFloat(values ...nullableFloat) nullableFloat {
	for _, value := range values {
		if value.Valid {
			return value
		}
	}
	return nullableFloat{}
}

func firstValidInt(values ...nullableInt) nullableInt {
	for _, value := range values {
		if value.Valid {
			return value
		}
	}
	return nullableInt{}
}

func (f nullableFloat) Float64() float64 {
	if !f.Valid {
		return 0
	}
	return f.Value
}

func (i nullableInt) Int() (int, bool) {
	return i.Value, i.Valid
}

func isAuthMessage(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "token") ||
		strings.Contains(msg, "login") ||
		strings.Contains(msg, "auth")
}

func isTurnstileMessage(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(msg, "turnstile") ||
		strings.Contains(msg, "captcha") ||
		strings.Contains(msg, "验证码") ||
		strings.Contains(msg, "人机")
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func baseHash(baseURL string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimRight(baseURL, "/"))))
	return hex.EncodeToString(sum[:])[:16]
}

func percent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	v := math.Round((used/limit*100)*10) / 10
	return math.Max(0, math.Min(100, v))
}
