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

	"quotaball/internal/config"
	"quotaball/internal/krill"
	"quotaball/internal/secret"
)

type Service struct {
	Config  *config.Config
	Secrets *secret.Store

	mu            sync.Mutex
	persistMu     sync.Mutex
	baseURL       string
	rememberLogin bool
	memToken      string
	sessionClient *Client
	email         string
	userID        string
	pending       *pendingOAuth
	authGen       uint64
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
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.baseURL != base {
		s.authGen++
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
	authGen := s.authGeneration()
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

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return OAuthStart{}, ErrAuthRequired
	}
	s.authGen++
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
	authGen := s.authGeneration()
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

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return OAuthStart{}, ErrAuthRequired
	}
	s.authGen++
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
	authGen := s.authGen
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
	if err := s.commitAuthIfCurrent(authGen, base, remember, user.Token, pending.client, email, userID); err != nil {
		return krill.EmptySnapshot(err.Error()), err
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
	authGen := s.authGeneration()
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
	if err := s.commitAuthIfCurrent(authGen, base, remember, "", client, snap.Email, userID); err != nil {
		return krill.EmptySnapshot(err.Error()), err
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
	authGen := s.authGeneration()
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
	if err := s.commitAuthIfCurrent(authGen, base, remember, token, nil, snap.Email, userID); err != nil {
		return krill.EmptySnapshot(err.Error()), err
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
	authGen := s.authGeneration()
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
	if err := s.commitAuthIfCurrent(authGen, base, remember, user.Token, client, snap.Email, userID); err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	return snap, nil
}

func (s *Service) Fetch(ctx context.Context) (krill.Snapshot, error) {
	base, authGen := s.currentBaseURLAndGeneration()
	if base == "" {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	token := s.loadTokenForGeneration(base, authGen)
	client, err := s.clientForFetch(base, token, authGen)
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
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.logoutIfCurrent(authGen, base)
		}
		return snap, err
	}
	if !s.authGenerationIs(authGen) {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	if err == nil && token == "" {
		var persistSession bool
		s.mu.Lock()
		if s.authGen != authGen {
			s.mu.Unlock()
			return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
		}
		s.sessionClient = client
		persistSession = s.rememberLogin && s.Secrets != nil
		s.mu.Unlock()
		if persistSession {
			if !s.saveAuthBestEffortIfCurrent(authGen, base, "", client, snap.Email, client.UserID) {
				return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
			}
		}
	}
	if !s.rememberEmailIfCurrent(snap.Email, authGen) {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	return snap, nil
}

func (s *Service) Logout() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	base := s.currentBaseURL()
	s.mu.Lock()
	s.authGen++
	s.memToken = ""
	s.sessionClient = nil
	s.email = ""
	s.userID = ""
	s.pending = nil
	s.mu.Unlock()
	if base != "" {
		_ = s.clearSaved(base)
	}
}

func (s *Service) ClearSavedLogin() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	base := s.currentBaseURL()
	s.mu.Lock()
	s.authGen++
	s.memToken = ""
	s.sessionClient = nil
	s.email = ""
	s.userID = ""
	s.pending = nil
	s.mu.Unlock()
	if base == "" {
		return nil
	}
	return s.clearSaved(base)
}

func (s *Service) authGeneration() uint64 {
	s.mu.Lock()
	gen := s.authGen
	s.mu.Unlock()
	return gen
}

func (s *Service) authGenerationIs(gen uint64) bool {
	s.mu.Lock()
	ok := s.authGen == gen
	s.mu.Unlock()
	return ok
}

func (s *Service) commitAuthIfCurrent(authGen uint64, base string, remember bool, token string, client *Client, email, userID string) error {
	userID = cleanUserID(firstNonEmpty(userID, clientUserID(client)))
	email = strings.TrimSpace(email)

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return ErrAuthRequired
	}
	s.mu.Unlock()

	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, token, client, email, userID); err != nil {
			return err
		}
	} else if err := s.clearSaved(base); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authGen != authGen {
		return ErrAuthRequired
	}
	s.authGen++
	s.baseURL = base
	s.rememberLogin = remember
	s.memToken = token
	if token == "" {
		s.sessionClient = client
	} else {
		s.sessionClient = nil
	}
	s.email = email
	s.userID = userID
	s.pending = nil
	return nil
}

func (s *Service) logoutIfCurrent(authGen uint64, base string) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return
	}
	s.authGen++
	s.memToken = ""
	s.sessionClient = nil
	s.email = ""
	s.userID = ""
	s.pending = nil
	s.mu.Unlock()
	if base != "" {
		_ = s.clearSaved(base)
	}
}

func (s *Service) fetchWith(ctx context.Context, client *Client, status Status, token, email string) (krill.Snapshot, error) {
	user, err := client.UserSelf(ctx, token)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap := user.ToSnapshot(status, time.Now())
	if snap.Email == "" {
		snap.Email = email
	}
	return snap, nil
}

func (s *Service) loadToken() string {
	base, authGen := s.currentBaseURLAndGeneration()
	return s.loadTokenForGeneration(base, authGen)
}

func (s *Service) loadTokenForGeneration(base string, authGen uint64) string {
	s.mu.Lock()
	if s.memToken != "" {
		token := s.memToken
		s.mu.Unlock()
		return token
	}
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
	if s.authGen != authGen {
		s.mu.Unlock()
		return ""
	}
	s.memToken = token
	s.mu.Unlock()
	return token
}

func (s *Service) clientForFetch(base, token string, authGen uint64) (*Client, error) {
	userID := s.loadUserIDForGeneration(base, authGen)
	if !s.authGenerationIs(authGen) {
		return nil, ErrAuthRequired
	}
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
	base, _ := s.currentBaseURLAndGeneration()
	return base
}

func (s *Service) currentBaseURLAndGeneration() (string, uint64) {
	s.mu.Lock()
	base := s.baseURL
	authGen := s.authGen
	s.mu.Unlock()
	if base != "" {
		return base, authGen
	}
	if s.Config == nil {
		return "", authGen
	}
	cfg := *s.Config
	cfg.Normalize()
	return cfg.NewAPIBaseURL, authGen
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

func (s *Service) clearSaved(baseURL string) error {
	if s.Secrets == nil || baseURL == "" {
		return nil
	}
	return s.Secrets.Update(map[string]string{
		tokenKey(baseURL):   "",
		sessionKey(baseURL): "",
		emailKey(baseURL):   "",
		userIDKey(baseURL):  "",
	})
}

func (s *Service) saveAuth(baseURL, token string, client *Client, email, userID string) error {
	if s.Secrets == nil || baseURL == "" {
		return nil
	}
	userID = cleanUserID(firstNonEmpty(userID, clientUserID(client)))
	updates := map[string]string{
		emailKey(baseURL):  strings.TrimSpace(email),
		userIDKey(baseURL): userID,
	}
	if token != "" {
		updates[tokenKey(baseURL)] = token
		updates[sessionKey(baseURL)] = ""
	} else if client != nil {
		cookies, err := client.ExportSessionCookies()
		if err != nil {
			return err
		}
		if strings.TrimSpace(cookies) == "" {
			return ErrAuthRequired
		}
		updates[sessionKey(baseURL)] = cookies
		updates[tokenKey(baseURL)] = ""
	}
	return s.Secrets.Update(updates)
}

func (s *Service) saveAuthBestEffortIfCurrent(authGen uint64, baseURL, token string, client *Client, email, userID string) bool {
	if s.Secrets == nil || baseURL == "" {
		return s.authGenerationIs(authGen)
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	current := s.authGen == authGen
	s.mu.Unlock()
	if !current {
		return false
	}
	_ = s.saveAuth(baseURL, token, client, email, userID)

	s.mu.Lock()
	current = s.authGen == authGen
	s.mu.Unlock()
	return current
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
	base, authGen := s.currentBaseURLAndGeneration()
	return s.loadUserIDForGeneration(base, authGen)
}

func (s *Service) loadUserIDForGeneration(base string, authGen uint64) string {
	s.mu.Lock()
	if s.userID != "" {
		userID := s.userID
		s.mu.Unlock()
		return userID
	}
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
	if s.authGen != authGen {
		s.mu.Unlock()
		return ""
	}
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
	_, authGen := s.currentBaseURLAndGeneration()
	s.rememberEmailIfCurrent(email, authGen)
}

func (s *Service) rememberEmailIfCurrent(email string, authGen uint64) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return s.authGenerationIs(authGen)
	}
	base := s.currentBaseURL()
	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return false
	}
	s.email = email
	remember := s.rememberLogin
	hasSecrets := s.Secrets != nil
	s.mu.Unlock()
	if remember && hasSecrets && base != "" {
		return s.updateSavedBestEffortIfCurrent(authGen, map[string]string{emailKey(base): email})
	}
	return s.authGenerationIs(authGen)
}

func (s *Service) updateSavedBestEffortIfCurrent(authGen uint64, updates map[string]string) bool {
	if s.Secrets == nil || len(updates) == 0 {
		return s.authGenerationIs(authGen)
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	current := s.authGen == authGen
	s.mu.Unlock()
	if !current {
		return false
	}
	_ = s.Secrets.Update(updates)

	s.mu.Lock()
	current = s.authGen == authGen
	s.mu.Unlock()
	return current
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
