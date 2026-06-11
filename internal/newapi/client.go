package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"krill_monitor/internal/krill"
)

const (
	DefaultQuotaPerUnit = 500000
	UserAgent           = krill.UserAgent
)

var ErrAuthRequired = errors.New("请登录 NewAPI")

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Status struct {
	SystemName       string  `json:"system_name"`
	LinuxDoOAuth     bool    `json:"linuxdo_oauth"`
	LinuxDoClientID  string  `json:"linuxdo_client_id"`
	QuotaPerUnit     float64 `json:"quota_per_unit"`
	QuotaDisplayType string  `json:"quota_display_type"`
}

type OAuthCallback struct {
	Code  string
	State string
}

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Token    string `json:"token"`
}

type UserSelf struct {
	ID           int     `json:"id"`
	Username     string  `json:"username"`
	DisplayName  string  `json:"display_name"`
	Email        string  `json:"email"`
	Quota        float64 `json:"quota"`
	UsedQuota    float64 `json:"used_quota"`
	RequestCount int     `json:"request_count"`
}

type TokenUsage struct {
	Name           string  `json:"name"`
	TotalGranted   float64 `json:"total_granted"`
	TotalUsed      float64 `json:"total_used"`
	TotalAvailable float64 `json:"total_available"`
	ExpiresAt      int64   `json:"expires_at"`
}

type apiResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
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
	if httpClient.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		httpClient.Jar = jar
	}
	return &Client{BaseURL: base, HTTPClient: httpClient}, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请输入 NewAPI 网站地址")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("NewAPI 网站地址无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("NewAPI 网站地址必须以 http:// 或 https:// 开头")
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", errors.New("NewAPI 网站地址必须使用 https://")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func LinuxDoAuthorizeURL(clientID, state string) string {
	u := url.URL{
		Scheme: "https",
		Host:   "connect.linux.do",
		Path:   "/oauth2/authorize",
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

func ExtractLinuxDoCallback(baseURL, callbackURL string) (OAuthCallback, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return OAuthCallback{}, err
	}
	baseParsed, err := url.Parse(base)
	if err != nil {
		return OAuthCallback{}, err
	}
	cb, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil || cb.Scheme == "" || cb.Host == "" {
		return OAuthCallback{}, errors.New("回调 URL 无效")
	}
	if !strings.EqualFold(cb.Host, baseParsed.Host) || cb.Path != "/oauth/linuxdo" {
		return OAuthCallback{}, errors.New("回调 URL 与 NewAPI 网站不匹配")
	}
	code := strings.TrimSpace(cb.Query().Get("code"))
	state := strings.TrimSpace(cb.Query().Get("state"))
	if code == "" || state == "" {
		return OAuthCallback{}, errors.New("回调 URL 缺少 code 或 state")
	}
	return OAuthCallback{Code: code, State: state}, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, http.MethodGet, "/api/status", "", nil, &status)
	return status, err
}

func (c *Client) OAuthState(ctx context.Context) (string, error) {
	var state string
	if err := c.do(ctx, http.MethodGet, "/api/oauth/state", "", nil, &state); err != nil {
		return "", err
	}
	if strings.TrimSpace(state) == "" {
		return "", errors.New("NewAPI 未返回 OAuth state")
	}
	return state, nil
}

func (c *Client) CompleteLinuxDoOAuth(ctx context.Context, code, state string) (User, error) {
	path := "/api/oauth/linuxdo?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	var user User
	if err := c.do(ctx, http.MethodGet, path, "", nil, &user); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(user.Token) == "" {
		return User{}, errors.New("NewAPI 登录成功但未返回用户 token")
	}
	return user, nil
}

func (c *Client) UserSelf(ctx context.Context, token string) (UserSelf, error) {
	var user UserSelf
	err := c.do(ctx, http.MethodGet, "/api/user/self", token, nil, &user)
	return user, err
}

func (c *Client) TokenUsage(ctx context.Context, token string) (TokenUsage, error) {
	var usage TokenUsage
	err := c.do(ctx, http.MethodGet, "/api/usage/token/", token, nil, &usage)
	return usage, err
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
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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

	var api apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return err
	}
	if !api.Success {
		msg := strings.TrimSpace(api.Message)
		if msg == "" {
			msg = "NewAPI 请求失败"
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
			strings.Contains(strings.ToLower(msg), "unauthorized") ||
			strings.Contains(strings.ToLower(msg), "token not provided") {
			return ErrAuthRequired
		}
		return fmt.Errorf("%s", msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(api.Data, out)
}

func (s Status) unit() float64 {
	if s.QuotaPerUnit > 0 {
		return s.QuotaPerUnit
	}
	return DefaultQuotaPerUnit
}

func (s Status) name() string {
	if strings.TrimSpace(s.SystemName) != "" {
		return strings.TrimSpace(s.SystemName)
	}
	return "NewAPI"
}

func (u UserSelf) ToSnapshot(status Status, now time.Time) krill.Snapshot {
	used := quotaToDisplay(u.UsedQuota, status)
	remaining := quotaToDisplay(u.Quota, status)
	total := used + remaining
	email := strings.TrimSpace(u.Email)
	if email == "" {
		email = strings.TrimSpace(u.Username)
	}
	subName := status.name() + " 账户额度"
	sub := krill.Subscription{
		ID:             strconv.Itoa(u.ID),
		Name:           subName,
		DaysLeft:       "长期",
		Start:          "-",
		End:            "-",
		DailyLimit:     total,
		DailyUsed:      used,
		DailyRemaining: remaining,
		DailyPercent:   percent(used, total),
	}
	return krill.Snapshot{
		Spend:  used,
		Wallet: remaining,
		Req:    requestCountText(u.RequestCount),
		Cache:  "-",
		Summary: krill.Summary{
			TotalUsedUSD:           used,
			TotalDailyQuotaUSD:     total,
			TotalRemainingUSD:      remaining,
			TotalDailyRemainingUSD: remaining,
		},
		Subscriptions: []krill.Subscription{sub},
		Time:          now,
		OK:            true,
		LoggedIn:      true,
		Email:         email,
	}
}

func (u TokenUsage) ToSnapshot(status Status, email string, now time.Time) krill.Snapshot {
	used := quotaToDisplay(u.TotalUsed, status)
	remaining := quotaToDisplay(u.TotalAvailable, status)
	total := quotaToDisplay(u.TotalGranted, status)
	if total <= 0 {
		total = used + remaining
	}
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = status.name() + " Token 额度"
	}
	sub := krill.Subscription{
		ID:             "newapi-token",
		Name:           name,
		DaysLeft:       tokenDaysLeft(u.ExpiresAt, now),
		Start:          "-",
		End:            tokenEnd(u.ExpiresAt),
		DailyLimit:     total,
		DailyUsed:      used,
		DailyRemaining: remaining,
		DailyPercent:   percent(used, total),
	}
	return krill.Snapshot{
		Spend:  used,
		Wallet: remaining,
		Req:    "-",
		Cache:  "-",
		Summary: krill.Summary{
			TotalUsedUSD:           used,
			TotalDailyQuotaUSD:     total,
			TotalRemainingUSD:      remaining,
			TotalDailyRemainingUSD: remaining,
		},
		Subscriptions: []krill.Subscription{sub},
		Time:          now,
		OK:            true,
		LoggedIn:      true,
		Email:         strings.TrimSpace(email),
	}
}

func quotaToDisplay(value float64, status Status) float64 {
	return value / status.unit()
}

func requestCountText(count int) string {
	if count <= 0 {
		return "-"
	}
	return strconv.Itoa(count)
}

func percent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	v := math.Round((used/limit*100)*10) / 10
	return math.Max(0, math.Min(100, v))
}

func tokenDaysLeft(expiresAt int64, now time.Time) any {
	if expiresAt <= 0 {
		return "长期"
	}
	end := time.Unix(expiresAt, 0)
	return int(end.Sub(now).Hours()/24) + 1
}

func tokenEnd(expiresAt int64) string {
	if expiresAt <= 0 {
		return "-"
	}
	return time.Unix(expiresAt, 0).Format("2006-01-02")
}
