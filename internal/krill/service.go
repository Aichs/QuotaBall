package krill

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"krill_monitor/internal/config"
	"krill_monitor/internal/secret"
)

type Service struct {
	Client    *Client
	Config    *config.Config
	Secrets   *secret.Store
	LegacyTok string

	mu             sync.Mutex
	memToken       string
	email          string
	rememberLogin  bool
	legacyPassword string
	configLoaded   bool
	authGen        uint64
}

func (s *Service) Configure(cfg config.Config) {
	s.mu.Lock()
	s.email = strings.TrimSpace(cfg.Email)
	s.rememberLogin = cfg.RememberLogin
	s.legacyPassword = cfg.Password
	s.configLoaded = true
	s.mu.Unlock()
}

func (s *Service) HasLoginState() bool {
	token, _ := s.loadToken()
	return token != "" || s.savedCredentialsOK()
}

func (s *Service) HasSavedLoginState() bool {
	return s.savedToken() != "" || s.savedCredentialsOK()
}

func (s *Service) Login(ctx context.Context, email, password string, remember bool) error {
	if strings.TrimSpace(email) == "" || password == "" {
		return errors.New("请输入邮箱和密码")
	}
	email = strings.TrimSpace(email)
	s.mu.Lock()
	authGen := s.authGen
	s.mu.Unlock()
	client := s.client()
	token, err := client.Login(ctx, email, password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return ErrAuthRequired
	}
	s.email = email
	s.rememberLogin = remember
	s.legacyPassword = ""
	s.configLoaded = true
	s.memToken = token
	if remember && s.Secrets != nil {
		if err := s.Secrets.Set("password", password); err != nil {
			s.mu.Unlock()
			return err
		}
		if err := s.Secrets.Set("token", token); err != nil {
			s.mu.Unlock()
			return err
		}
	} else if s.Secrets != nil {
		_ = s.Secrets.Set("password", "")
		_ = s.Secrets.Set("token", "")
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) Logout() {
	s.mu.Lock()
	s.authGen++
	s.memToken = ""
	s.legacyPassword = ""
	s.mu.Unlock()
	if s.Secrets != nil {
		_ = s.Secrets.Set("token", "")
		_ = s.Secrets.Set("password", "")
	}
	if s.LegacyTok != "" {
		_ = os.Remove(s.LegacyTok)
	}
}

func (s *Service) ClearSavedLogin() {
	s.mu.Lock()
	s.rememberLogin = false
	s.legacyPassword = ""
	s.configLoaded = true
	s.mu.Unlock()
	if s.Secrets != nil {
		_ = s.Secrets.Set("token", "")
		_ = s.Secrets.Set("password", "")
	}
	if s.LegacyTok != "" {
		_ = os.Remove(s.LegacyTok)
	}
}

func (s *Service) Fetch(ctx context.Context) (Snapshot, error) {
	token, authGen, err := s.authToken(ctx)
	if err != nil {
		return EmptySnapshot(ErrAuthRequired.Error()), err
	}
	payload, err := s.client().Subscription(ctx, token)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.Logout()
			return EmptySnapshot(ErrAuthRequired.Error()), err
		}
		snap := EmptySnapshot(err.Error())
		return snap, err
	}
	if !s.authGenerationIs(authGen) {
		return EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	snap := payload.ToSnapshot(time.Now())
	snap.Email = s.emailSnapshot()
	return snap, nil
}

func (s *Service) authToken(ctx context.Context) (string, uint64, error) {
	token, authGen := s.loadToken()
	if token != "" && !JWTExpired(token, time.Now()) {
		return token, authGen, nil
	}
	if token != "" {
		s.clearTokenOnly()
	}
	email, password, authGen, ok := s.savedCredentialsForAuth()
	if !ok {
		return "", authGen, ErrAuthRequired
	}
	token, err := s.client().Login(ctx, email, password)
	if err != nil {
		return "", authGen, err
	}
	if !s.saveTokenIfCurrent(token, authGen) {
		return "", authGen, ErrAuthRequired
	}
	return token, authGen, nil
}

func (s *Service) loadToken() (string, uint64) {
	s.mu.Lock()
	if s.memToken != "" {
		token := s.memToken
		authGen := s.authGen
		s.mu.Unlock()
		return token, authGen
	}
	remember := s.rememberLoginLocked()
	authGen := s.authGen
	s.mu.Unlock()

	if !remember {
		return "", authGen
	}
	if token := s.savedToken(); token != "" {
		if !s.saveTokenIfCurrent(token, authGen) {
			return "", authGen
		}
		if s.LegacyTok != "" {
			_ = os.Remove(s.LegacyTok)
		}
		return token, authGen
	}
	return "", authGen
}

func (s *Service) savedToken() string {
	s.mu.Lock()
	remember := s.rememberLoginLocked()
	s.mu.Unlock()
	if !remember {
		return ""
	}
	if s.Secrets != nil {
		if token, err := s.Secrets.Get("token"); err == nil && token != "" {
			return token
		}
	}
	if s.LegacyTok != "" {
		if raw, err := os.ReadFile(s.LegacyTok); err == nil {
			token := strings.TrimSpace(string(raw))
			if token != "" {
				return token
			}
		}
	}
	return ""
}

func (s *Service) saveToken(token string) {
	_ = s.saveTokenIfCurrent(token, s.authGeneration())
}

func (s *Service) saveTokenIfCurrent(token string, authGen uint64) bool {
	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return false
	}
	s.memToken = token
	remember := s.rememberLoginLocked()
	if remember && s.Secrets != nil {
		_ = s.Secrets.Set("token", token)
	}
	s.mu.Unlock()
	return true
}

func (s *Service) clearTokenOnly() {
	s.mu.Lock()
	s.memToken = ""
	s.mu.Unlock()
	if s.Secrets != nil {
		_ = s.Secrets.Set("token", "")
	}
	if s.LegacyTok != "" {
		_ = os.Remove(s.LegacyTok)
	}
}

func (s *Service) savedCredentialsOK() bool {
	_, _, ok := s.savedCredentials()
	return ok
}

func (s *Service) savedCredentials() (string, string, bool) {
	email, password, _, ok := s.savedCredentialsForAuth()
	return email, password, ok
}

func (s *Service) savedCredentialsForAuth() (string, string, uint64, bool) {
	s.mu.Lock()
	remember := s.rememberLoginLocked()
	email := strings.TrimSpace(s.email)
	password := s.legacyPassword
	authGen := s.authGen
	s.mu.Unlock()
	if !remember {
		return "", "", authGen, false
	}
	if s.Secrets != nil {
		if saved, _ := s.Secrets.Get("password"); saved != "" {
			password = saved
		}
	}
	return email, password, authGen, email != "" && password != ""
}

func (s *Service) rememberLoginLocked() bool {
	if !s.configLoaded && s.Config != nil {
		s.email = strings.TrimSpace(s.Config.Email)
		s.rememberLogin = s.Config.RememberLogin
		s.legacyPassword = s.Config.Password
		s.configLoaded = true
	}
	return s.rememberLogin
}

func (s *Service) emailSnapshot() string {
	s.mu.Lock()
	_ = s.rememberLoginLocked()
	email := s.email
	s.mu.Unlock()
	return email
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

func (s *Service) client() *Client {
	if s.Client != nil {
		return s.Client
	}
	s.Client = NewClient()
	return s.Client
}
