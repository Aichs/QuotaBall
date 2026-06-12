package newapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
	"krill_monitor/internal/secret"
)

type Service struct {
	Config  *config.Config
	Secrets *secret.Store

	mu            sync.Mutex
	baseURL       string
	rememberLogin bool
	memToken      string
	sessionClient *Client
	email         string
	userID        string
	pending       *pendingOAuth
}

type OAuthStart struct {
	BaseURL         string
	AuthorizeURL    string
	LinuxDoClientID string
}

type pendingOAuth struct {
	baseURL string
	state   string
	client  *Client
	status  Status
}

func (s *Service) Configure(cfg config.Config) {
	cfg.Normalize()
	base := strings.TrimSpace(cfg.NewAPIBaseURL)
	s.mu.Lock()
	if s.baseURL != base {
		s.memToken = ""
		s.sessionClient = nil
		s.email = ""
		s.userID = ""
		s.pending = nil
	}
	s.baseURL = base
	s.rememberLogin = cfg.RememberLogin
	s.mu.Unlock()
}

func (s *Service) HasLoginState() bool {
	if s.loadToken() != "" && s.loadUserID() != "" {
		return true
	}
	s.mu.Lock()
	hasSession := s.sessionClient != nil && strings.TrimSpace(s.sessionClient.UserID) != ""
	base := s.baseURL
	remember := s.rememberLogin
	s.mu.Unlock()
	return hasSession || remember && base != "" && s.savedSessionCookies(base) != "" && s.savedUserID(base) != ""
}

func (s *Service) HasSavedLoginState() bool {
	base := s.currentBaseURL()
	return base != "" && s.savedUserID(base) != "" && (s.savedToken(base) != "" || s.savedSessionCookies(base) != "")
}

func (s *Service) StartLinuxDo(ctx context.Context, baseURL string, remember bool) (OAuthStart, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return OAuthStart{}, err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return OAuthStart{}, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return OAuthStart{}, err
	}
	if !status.LinuxDoOAuth || strings.TrimSpace(status.LinuxDoClientID) == "" {
		return OAuthStart{}, errors.New("该 NewAPI 站点未启用 LinuxDo 登录")
	}
	_ = client.Logout(ctx)
	state, err := client.OAuthState(ctx)
	if err != nil {
		return OAuthStart{}, err
	}

	s.mu.Lock()
	if s.baseURL != base {
		s.memToken = ""
		s.sessionClient = nil
		s.email = ""
		s.userID = ""
	}
	s.baseURL = base
	s.rememberLogin = remember
	s.pending = &pendingOAuth{
		baseURL: base,
		state:   state,
		client:  client,
		status:  status,
	}
	s.mu.Unlock()

	return OAuthStart{
		BaseURL:         base,
		AuthorizeURL:    LinuxDoAuthorizeURL(status.LinuxDoClientID, state),
		LinuxDoClientID: status.LinuxDoClientID,
	}, nil
}

func (s *Service) StartLinuxDoBrowser(ctx context.Context, baseURL string, remember bool) (OAuthStart, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return OAuthStart{}, err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return OAuthStart{}, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return OAuthStart{}, err
	}
	clientID := strings.TrimSpace(status.LinuxDoClientID)
	if !status.LinuxDoOAuth || clientID == "" {
		return OAuthStart{}, errors.New("该 NewAPI 站点未启用 LinuxDo 登录")
	}

	s.mu.Lock()
	if s.baseURL != base {
		s.memToken = ""
		s.sessionClient = nil
		s.email = ""
		s.userID = ""
	}
	s.baseURL = base
	s.rememberLogin = remember
	s.pending = nil
	s.mu.Unlock()

	return OAuthStart{
		BaseURL:         base,
		LinuxDoClientID: clientID,
	}, nil
}

func (s *Service) CompleteLinuxDo(ctx context.Context, baseURL, callbackURL string, remember bool) (krill.Snapshot, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	cb, err := ExtractLinuxDoCallback(base, callbackURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}

	s.mu.Lock()
	pending := s.pending
	if pending == nil || pending.baseURL != base {
		s.mu.Unlock()
		err := errors.New("请先打开 LinuxDo 登录页面")
		return krill.EmptySnapshot(err.Error()), err
	}
	if pending.state != cb.State {
		s.mu.Unlock()
		err := errors.New("OAuth state 不匹配，请重新登录")
		return krill.EmptySnapshot(err.Error()), err
	}
	s.mu.Unlock()

	user, err := pending.client.CompleteLinuxDoOAuth(ctx, cb.Code, cb.State)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	userID := userIDFromInt(user.ID)
	pending.client.UserID = userID
	email := firstNonEmpty(user.Email, user.DisplayName, user.Username)
	snap, err := s.fetchWith(ctx, pending.client, pending.status, user.Token, email)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	s.mu.Lock()
	s.baseURL = base
	s.rememberLogin = remember
	s.memToken = user.Token
	if user.Token == "" {
		s.sessionClient = pending.client
	} else {
		s.sessionClient = nil
	}
	s.email = email
	s.userID = userID
	s.pending = nil
	s.mu.Unlock()
	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, user.Token, pending.client, email, userID); err != nil {
			return krill.EmptySnapshot(err.Error()), err
		}
	} else {
		s.clearSaved(base)
	}
	return snap, nil
}

func (s *Service) CompleteBrowserSession(ctx context.Context, baseURL, sessionCookies, userID string, remember bool) (krill.Snapshot, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	if strings.TrimSpace(sessionCookies) == "" {
		err := errors.New("NewAPI 自动登录未返回 session")
		return krill.EmptySnapshot(err.Error()), err
	}
	userID = cleanUserID(userID)
	if userID == "" {
		err := errors.New("NewAPI 自动登录未返回用户 ID")
		return krill.EmptySnapshot(err.Error()), err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	client.UserID = userID
	if err := client.ImportSessionCookies(sessionCookies); err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap, err := s.fetchWith(ctx, client, status, "", "")
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	s.mu.Lock()
	s.baseURL = base
	s.rememberLogin = remember
	s.memToken = ""
	s.sessionClient = client
	s.email = snap.Email
	s.userID = userID
	s.pending = nil
	s.mu.Unlock()
	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, "", client, snap.Email, userID); err != nil {
			return krill.EmptySnapshot(err.Error()), err
		}
	} else {
		s.clearSaved(base)
	}
	return snap, nil
}

func (s *Service) CompleteBrowserToken(ctx context.Context, baseURL, token, userID string, remember bool) (krill.Snapshot, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		err := errors.New("NewAPI 自动登录未返回 token")
		return krill.EmptySnapshot(err.Error()), err
	}
	userID = cleanUserID(userID)
	if userID == "" {
		err := errors.New("NewAPI 自动登录未返回用户 ID")
		return krill.EmptySnapshot(err.Error()), err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	client.UserID = userID
	status, err := client.Status(ctx)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap, err := s.fetchWith(ctx, client, status, token, "")
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	s.mu.Lock()
	s.baseURL = base
	s.rememberLogin = remember
	s.memToken = token
	s.sessionClient = nil
	s.email = snap.Email
	s.userID = userID
	s.pending = nil
	s.mu.Unlock()
	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, token, nil, snap.Email, userID); err != nil {
			return krill.EmptySnapshot(err.Error()), err
		}
	} else {
		s.clearSaved(base)
	}
	return snap, nil
}

func (s *Service) CompleteLinuxDoWithCookies(ctx context.Context, baseURL, callbackURL, sessionCookies string, remember bool) (krill.Snapshot, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	cb, err := ExtractLinuxDoCallback(base, callbackURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	if strings.TrimSpace(sessionCookies) == "" {
		err := errors.New("NewAPI 自动登录未返回 state cookie")
		return krill.EmptySnapshot(err.Error()), err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	if err := client.ImportSessionCookies(sessionCookies); err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	user, err := client.CompleteLinuxDoOAuth(ctx, cb.Code, cb.State)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	userID := userIDFromInt(user.ID)
	client.UserID = userID
	email := firstNonEmpty(user.Email, user.DisplayName, user.Username)
	snap, err := s.fetchWith(ctx, client, status, user.Token, email)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	s.mu.Lock()
	s.baseURL = base
	s.rememberLogin = remember
	s.memToken = user.Token
	if user.Token == "" {
		s.sessionClient = client
	} else {
		s.sessionClient = nil
	}
	s.email = snap.Email
	s.userID = userID
	s.pending = nil
	s.mu.Unlock()
	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, user.Token, client, snap.Email, userID); err != nil {
			return krill.EmptySnapshot(err.Error()), err
		}
	} else {
		s.clearSaved(base)
	}
	return snap, nil
}

func (s *Service) Fetch(ctx context.Context) (krill.Snapshot, error) {
	base := s.currentBaseURL()
	if base == "" {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	token := s.loadToken()
	client, err := s.clientForFetch(base, token)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	if client == nil {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	status, err := client.Status(ctx)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	email := s.emailSnapshot(base)
	snap, err := s.fetchWith(ctx, client, status, token, email)
	if err == nil && token == "" {
		s.mu.Lock()
		s.sessionClient = client
		remember := s.rememberLogin
		s.mu.Unlock()
		if remember && s.Secrets != nil {
			_ = s.saveAuth(base, "", client, snap.Email, client.UserID)
		}
	}
	return snap, err
}

func (s *Service) Logout() {
	base := s.currentBaseURL()
	s.mu.Lock()
	s.memToken = ""
	s.sessionClient = nil
	s.email = ""
	s.userID = ""
	s.pending = nil
	s.mu.Unlock()
	if base != "" {
		s.clearSaved(base)
	}
}

func (s *Service) ClearSavedLogin() {
	s.Logout()
}

func (s *Service) fetchWith(ctx context.Context, client *Client, status Status, token, email string) (krill.Snapshot, error) {
	user, err := client.UserSelf(ctx, token)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.Logout()
		}
		return krill.EmptySnapshot(err.Error()), err
	}
	snap := user.ToSnapshot(status, time.Now())
	if snap.Email == "" {
		snap.Email = email
	}
	s.rememberEmail(snap.Email)
	return snap, nil
}

func (s *Service) loadToken() string {
	s.mu.Lock()
	if s.memToken != "" {
		token := s.memToken
		s.mu.Unlock()
		return token
	}
	base := s.baseURL
	remember := s.rememberLogin
	s.mu.Unlock()
	if !remember || base == "" {
		return ""
	}
	token := s.savedToken(base)
	if token == "" {
		return ""
	}
	s.mu.Lock()
	s.memToken = token
	s.mu.Unlock()
	return token
}

func (s *Service) clientForFetch(base, token string) (*Client, error) {
	userID := s.loadUserID()
	if token != "" {
		client, err := NewClient(base, nil)
		if err != nil {
			return nil, err
		}
		client.UserID = userID
		return client, nil
	}
	s.mu.Lock()
	client := s.sessionClient
	remember := s.rememberLogin
	s.mu.Unlock()
	if client != nil {
		if strings.TrimSpace(client.UserID) == "" {
			client.UserID = userID
		}
		return client, nil
	}
	if !remember || userID == "" {
		return nil, nil
	}
	raw := s.savedSessionCookies(base)
	if raw == "" {
		return nil, nil
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return nil, err
	}
	if err := client.ImportSessionCookies(raw); err != nil {
		return nil, err
	}
	client.UserID = userID
	return client, nil
}

func (s *Service) currentBaseURL() string {
	s.mu.Lock()
	base := s.baseURL
	s.mu.Unlock()
	if base != "" {
		return base
	}
	if s.Config == nil {
		return ""
	}
	cfg := *s.Config
	cfg.Normalize()
	return cfg.NewAPIBaseURL
}

func (s *Service) savedToken(baseURL string) string {
	if s.Secrets == nil || baseURL == "" {
		return ""
	}
	token, err := s.Secrets.Get(tokenKey(baseURL))
	if err != nil {
		return ""
	}
	return token
}

func (s *Service) clearSaved(baseURL string) {
	if s.Secrets == nil || baseURL == "" {
		return
	}
	_ = s.Secrets.Set(tokenKey(baseURL), "")
	_ = s.Secrets.Set(sessionKey(baseURL), "")
	_ = s.Secrets.Set(emailKey(baseURL), "")
	_ = s.Secrets.Set(userIDKey(baseURL), "")
}

func (s *Service) saveAuth(baseURL, token string, client *Client, email, userID string) error {
	if s.Secrets == nil || baseURL == "" {
		return nil
	}
	userID = cleanUserID(firstNonEmpty(userID, clientUserID(client)))
	if token != "" {
		if err := s.Secrets.Set(tokenKey(baseURL), token); err != nil {
			return err
		}
		_ = s.Secrets.Set(sessionKey(baseURL), "")
	} else if client != nil {
		cookies, err := client.ExportSessionCookies()
		if err != nil {
			return err
		}
		if strings.TrimSpace(cookies) == "" {
			return ErrAuthRequired
		}
		if err := s.Secrets.Set(sessionKey(baseURL), cookies); err != nil {
			return err
		}
		_ = s.Secrets.Set(tokenKey(baseURL), "")
	}
	if userID != "" {
		if err := s.Secrets.Set(userIDKey(baseURL), userID); err != nil {
			return err
		}
	} else {
		_ = s.Secrets.Set(userIDKey(baseURL), "")
	}
	_ = s.Secrets.Set(emailKey(baseURL), strings.TrimSpace(email))
	return nil
}

func (s *Service) savedSessionCookies(baseURL string) string {
	if s.Secrets == nil || baseURL == "" {
		return ""
	}
	cookies, err := s.Secrets.Get(sessionKey(baseURL))
	if err != nil {
		return ""
	}
	return cookies
}

func (s *Service) loadUserID() string {
	s.mu.Lock()
	if s.userID != "" {
		userID := s.userID
		s.mu.Unlock()
		return userID
	}
	base := s.baseURL
	remember := s.rememberLogin
	s.mu.Unlock()
	if !remember || base == "" {
		return ""
	}
	userID := s.savedUserID(base)
	if userID == "" {
		return ""
	}
	s.mu.Lock()
	s.userID = userID
	s.mu.Unlock()
	return userID
}

func (s *Service) savedUserID(baseURL string) string {
	if s.Secrets == nil || baseURL == "" {
		return ""
	}
	userID, err := s.Secrets.Get(userIDKey(baseURL))
	if err != nil {
		return ""
	}
	return cleanUserID(userID)
}

func (s *Service) rememberEmail(email string) {
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	base := s.currentBaseURL()
	s.mu.Lock()
	s.email = email
	remember := s.rememberLogin
	s.mu.Unlock()
	if remember && s.Secrets != nil && base != "" {
		_ = s.Secrets.Set(emailKey(base), email)
	}
}

func (s *Service) emailSnapshot(baseURL string) string {
	s.mu.Lock()
	email := s.email
	s.mu.Unlock()
	if email != "" || s.Secrets == nil || baseURL == "" {
		return email
	}
	saved, _ := s.Secrets.Get(emailKey(baseURL))
	return saved
}

func tokenKey(baseURL string) string {
	return "newapi:" + baseHash(baseURL) + ":token"
}

func sessionKey(baseURL string) string {
	return "newapi:" + baseHash(baseURL) + ":session"
}

func emailKey(baseURL string) string {
	return "newapi:" + baseHash(baseURL) + ":email"
}

func userIDKey(baseURL string) string {
	return "newapi:" + baseHash(baseURL) + ":user_id"
}

func baseHash(baseURL string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimRight(baseURL, "/"))))
	return hex.EncodeToString(sum[:])[:16]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func userIDFromInt(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func cleanUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	id, err := strconv.Atoi(userID)
	if err != nil || id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func clientUserID(client *Client) string {
	if client == nil {
		return ""
	}
	return client.UserID
}
