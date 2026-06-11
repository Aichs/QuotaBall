package newapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	pending       *pendingOAuth
}

type OAuthStart struct {
	BaseURL      string
	AuthorizeURL string
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
		s.pending = nil
	}
	s.baseURL = base
	s.rememberLogin = cfg.RememberLogin
	s.mu.Unlock()
}

func (s *Service) HasLoginState() bool {
	if s.loadToken() != "" {
		return true
	}
	s.mu.Lock()
	hasSession := s.sessionClient != nil
	base := s.baseURL
	remember := s.rememberLogin
	s.mu.Unlock()
	return hasSession || remember && base != "" && s.savedSessionCookies(base) != ""
}

func (s *Service) HasSavedLoginState() bool {
	base := s.currentBaseURL()
	return base != "" && (s.savedToken(base) != "" || s.savedSessionCookies(base) != "")
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
		BaseURL:      base,
		AuthorizeURL: LinuxDoAuthorizeURL(status.LinuxDoClientID, state),
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
	s.pending = nil
	s.mu.Unlock()
	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, user.Token, pending.client, email); err != nil {
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
			_ = s.saveAuth(base, "", client, snap.Email)
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
	if token != "" {
		return NewClient(base, nil)
	}
	s.mu.Lock()
	client := s.sessionClient
	remember := s.rememberLogin
	s.mu.Unlock()
	if client != nil {
		return client, nil
	}
	if !remember {
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
}

func (s *Service) saveAuth(baseURL, token string, client *Client, email string) error {
	if s.Secrets == nil || baseURL == "" {
		return nil
	}
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
