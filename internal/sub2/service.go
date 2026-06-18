package sub2

import (
	"context"
	"errors"
	"math"
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
	refreshToken  string
	email         string
	expiresAt     time.Time
	authGen       uint64
}

func (s *Service) Configure(cfg config.Config) {
	cfg.Normalize()
	base := strings.TrimSpace(cfg.Sub2BaseURL)
	if base != "" {
		if normalized, err := NormalizeBaseURL(base); err == nil {
			base = normalized
		}
	}
	email := strings.TrimSpace(cfg.Sub2Email)

	s.mu.Lock()
	if s.baseURL != base {
		s.authGen++
		s.memToken = ""
		s.refreshToken = ""
		s.expiresAt = time.Time{}
	}
	s.baseURL = base
	s.email = email
	s.rememberLogin = cfg.RememberLogin
	s.mu.Unlock()
}

func (s *Service) HasLoginState() bool {
	base, authGen := s.currentBaseURLAndGeneration()
	if base == "" {
		return false
	}
	if s.loadTokenForGeneration(base, authGen) != "" {
		return true
	}
	s.mu.Lock()
	remember := s.rememberLogin
	s.mu.Unlock()
	return remember && (s.savedRefreshToken(base) != "" || s.savedPassword(base) != "")
}

func (s *Service) HasSavedLoginState() bool {
	base := s.currentBaseURL()
	return base != "" && (s.savedToken(base) != "" || s.savedRefreshToken(base) != "" || s.savedPassword(base) != "")
}

func (s *Service) Login(ctx context.Context, baseURL, email, password string, remember bool) (krill.Snapshot, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		err := errors.New("请输入邮箱和密码")
		return krill.EmptySnapshot(err.Error()), err
	}
	authGen := s.authGeneration()
	client, err := NewClient(base, nil)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	auth, err := client.Login(ctx, email, password)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	if err := s.commitAuthIfCurrent(authGen, base, email, password, remember, auth); err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap, err := s.Fetch(ctx)
	if err != nil {
		return snap, err
	}
	return snap, nil
}

func (s *Service) Fetch(ctx context.Context) (krill.Snapshot, error) {
	base, authGen := s.currentBaseURLAndGeneration()
	if base == "" {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
	}
	token, err := s.authToken(ctx, base, authGen)
	if err != nil {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), err
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap, err := s.fetchWith(ctx, client, token)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.logoutIfCurrent(authGen, base)
		}
		return snap, err
	}
	if !s.authGenerationIs(authGen) {
		return krill.EmptySnapshot(ErrAuthRequired.Error()), ErrAuthRequired
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
	refresh := s.currentRefreshToken(base)
	if base != "" && refresh != "" {
		if client, err := NewClient(base, nil); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = client.Logout(ctx, refresh)
			cancel()
		}
	}
	s.mu.Lock()
	s.authGen++
	s.memToken = ""
	s.refreshToken = ""
	s.expiresAt = time.Time{}
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
	s.refreshToken = ""
	s.expiresAt = time.Time{}
	s.rememberLogin = false
	s.mu.Unlock()
	if base == "" {
		return nil
	}
	return s.clearSaved(base)
}

func (s *Service) authToken(ctx context.Context, base string, authGen uint64) (string, error) {
	if token := s.loadTokenForGeneration(base, authGen); token != "" {
		return token, nil
	}
	if refresh := s.currentRefreshToken(base); refresh != "" {
		client, err := NewClient(base, nil)
		if err != nil {
			return "", err
		}
		auth, err := client.Refresh(ctx, refresh)
		if err == nil {
			if !s.saveRefreshedAuthIfCurrent(authGen, base, auth) {
				return "", ErrAuthRequired
			}
			return auth.AccessToken, nil
		}
		if !errors.Is(err, ErrAuthRequired) {
			return "", err
		}
	}

	email, password, ok := s.savedCredentials(base)
	if !ok {
		return "", ErrAuthRequired
	}
	client, err := NewClient(base, nil)
	if err != nil {
		return "", err
	}
	auth, err := client.Login(ctx, email, password)
	if err != nil {
		return "", err
	}
	if !s.saveLoginAuthIfCurrent(authGen, base, email, password, auth) {
		return "", ErrAuthRequired
	}
	return auth.AccessToken, nil
}

func (s *Service) fetchWith(ctx context.Context, client *Client, token string) (krill.Snapshot, error) {
	user, err := client.Profile(ctx, token)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	summary, err := client.SubscriptionSummary(ctx, token)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	progress, err := client.SubscriptionProgress(ctx, token)
	if err != nil {
		return krill.EmptySnapshot(err.Error()), err
	}
	snap := ToSnapshot(user, summary, progress, time.Now())
	return snap, nil
}

func ToSnapshot(user User, summary SubscriptionSummary, progress []SubscriptionProgressInfo, now time.Time) krill.Snapshot {
	summaryByID := make(map[int64]SubscriptionSummaryItem, len(summary.Subscriptions))
	for _, item := range summary.Subscriptions {
		summaryByID[item.ID] = item
	}
	subscriptions := make([]krill.Subscription, 0, maxInt(len(progress), len(summary.Subscriptions)))
	seen := make(map[int64]bool)
	for _, item := range progress {
		sub := subscriptionFromProgress(item, summaryByID, now)
		if sub.ID == "" {
			continue
		}
		if id, err := strconv.ParseInt(sub.ID, 10, 64); err == nil {
			seen[id] = true
		}
		subscriptions = append(subscriptions, sub)
	}
	for _, item := range summary.Subscriptions {
		if seen[item.ID] {
			continue
		}
		subscriptions = append(subscriptions, subscriptionFromSummary(item, now))
	}
	dailyLimit, dailyUsed, weeklyLimit, weeklyUsed, monthlyLimit, monthlyUsed := aggregateSubscriptionTotals(subscriptions)
	email := strings.TrimSpace(user.Email)
	if email == "" {
		email = strings.TrimSpace(user.Username)
	}
	return krill.Snapshot{
		Spend:    monthlyUsed,
		Wallet:   user.Balance,
		Req:      "-",
		Cache:    "-",
		Provider: config.ProviderSub2,
		Summary: krill.Summary{
			TotalUsedUSD:             monthlyUsed,
			TotalDailyQuotaUSD:       dailyLimit,
			TotalDailyUsedUSD:        dailyUsed,
			TotalDailyRemainingUSD:   math.Max(0, dailyLimit-dailyUsed),
			TotalRemainingUSD:        math.Max(0, weeklyLimit-weeklyUsed),
			TotalWeeklyQuotaUSD:      weeklyLimit,
			TotalWeeklyUsedUSD:       weeklyUsed,
			TotalWeeklyRemainingUSD:  math.Max(0, weeklyLimit-weeklyUsed),
			TotalMonthlyQuotaUSD:     monthlyLimit,
			TotalMonthlyUsedUSD:      monthlyUsed,
			TotalMonthlyRemainingUSD: math.Max(0, monthlyLimit-monthlyUsed),
		},
		Subscriptions: subscriptions,
		Time:          now,
		OK:            true,
		LoggedIn:      true,
		Email:         email,
	}
}

func aggregateSubscriptionTotals(subscriptions []krill.Subscription) (dailyLimit, dailyUsed, weeklyLimit, weeklyUsed, monthlyLimit, monthlyUsed float64) {
	for _, sub := range subscriptions {
		dailyLimit += sub.DailyLimit
		dailyUsed += sub.DailyUsed
		weeklyLimit += sub.WeeklyLimit
		weeklyUsed += sub.WeeklyUsed
		monthlyLimit += sub.MonthlyLimit
		monthlyUsed += sub.MonthlyUsed
	}
	return dailyLimit, dailyUsed, weeklyLimit, weeklyUsed, monthlyLimit, monthlyUsed
}

func subscriptionFromProgress(item SubscriptionProgressInfo, summaries map[int64]SubscriptionSummaryItem, now time.Time) krill.Subscription {
	var sub UserSubscription
	if item.Subscription != nil {
		sub = *item.Subscription
	}
	var progress SubscriptionProgress
	if item.Progress != nil {
		progress = *item.Progress
	}
	id := firstPositiveInt(sub.ID, progress.SubscriptionID)
	summary := summaries[id]
	daily := quotaFromWindow(progress.Daily, sub.DailyUsageUSD, firstPositiveFloat(sub.groupDailyLimit(), summary.DailyLimitUSD))
	weekly := quotaFromWindow(progress.Weekly, sub.WeeklyUsageUSD, firstPositiveFloat(sub.groupWeeklyLimit(), summary.WeeklyLimitUSD))
	monthly := quotaFromWindow(progress.Monthly, sub.MonthlyUsageUSD, firstPositiveFloat(sub.groupMonthlyLimit(), summary.MonthlyLimitUSD))
	name := firstNonEmpty(sub.groupName(), summary.GroupName, "Sub2 订阅")
	end := firstNonEmpty(progress.ExpiresAt, sub.ExpiresAt, summary.ExpiresAt)
	daysLeft := any("?")
	if v, ok := progress.DaysRemaining.Int(); ok {
		daysLeft = v
	} else if parsed, ok := daysUntil(end, now); ok {
		daysLeft = parsed
	}
	return krill.Subscription{
		ID:               strconv.FormatInt(id, 10),
		Name:             name,
		DaysLeft:         daysLeft,
		Start:            datePart(sub.StartsAt),
		End:              datePart(end),
		Routes:           routesForGroup(sub.Group),
		DailyLimit:       daily.limit,
		DailyUsed:        daily.used,
		DailyRemaining:   daily.remaining,
		DailyPercent:     daily.percent,
		WeeklyLimit:      weekly.limit,
		WeeklyUsed:       weekly.used,
		WeeklyRemaining:  weekly.remaining,
		WeeklyPercent:    weekly.percent,
		MonthlyLimit:     monthly.limit,
		MonthlyUsed:      monthly.used,
		MonthlyRemaining: monthly.remaining,
		MonthlyPercent:   monthly.percent,
	}
}

func subscriptionFromSummary(item SubscriptionSummaryItem, now time.Time) krill.Subscription {
	daysLeft := any("?")
	if parsed, ok := daysUntil(item.ExpiresAt, now); ok {
		daysLeft = parsed
	}
	return krill.Subscription{
		ID:               strconv.FormatInt(item.ID, 10),
		Name:             firstNonEmpty(item.GroupName, "Sub2 订阅"),
		DaysLeft:         daysLeft,
		End:              datePart(item.ExpiresAt),
		DailyLimit:       item.DailyLimitUSD,
		DailyUsed:        item.DailyUsedUSD,
		DailyRemaining:   math.Max(0, item.DailyLimitUSD-item.DailyUsedUSD),
		DailyPercent:     percent(item.DailyUsedUSD, item.DailyLimitUSD),
		WeeklyLimit:      item.WeeklyLimitUSD,
		WeeklyUsed:       item.WeeklyUsedUSD,
		WeeklyRemaining:  math.Max(0, item.WeeklyLimitUSD-item.WeeklyUsedUSD),
		WeeklyPercent:    percent(item.WeeklyUsedUSD, item.WeeklyLimitUSD),
		MonthlyLimit:     item.MonthlyLimitUSD,
		MonthlyUsed:      item.MonthlyUsedUSD,
		MonthlyRemaining: math.Max(0, item.MonthlyLimitUSD-item.MonthlyUsedUSD),
		MonthlyPercent:   percent(item.MonthlyUsedUSD, item.MonthlyLimitUSD),
	}
}

type quotaValues struct {
	used      float64
	limit     float64
	remaining float64
	percent   float64
}

func quotaFromWindow(w *QuotaWindow, fallbackUsed, fallbackLimit float64) quotaValues {
	used := fallbackUsed
	limit := fallbackLimit
	pct := percent(used, limit)
	remaining := math.Max(0, limit-used)
	if w != nil {
		if w.usedSet || w.Used != 0 {
			used = w.Used
		}
		if w.Limit.Valid {
			limit = w.Limit.Value
		}
		if limit == 0 && w.Remaining.Valid {
			limit = used + w.Remaining.Value
		}
		if w.Remaining.Valid {
			remaining = math.Max(0, w.Remaining.Value)
		} else {
			remaining = math.Max(0, limit-used)
		}
		if w.Percentage > 0 {
			pct = math.Max(0, math.Min(100, w.Percentage))
		} else {
			pct = percent(used, limit)
		}
	}
	return quotaValues{used: used, limit: limit, remaining: remaining, percent: pct}
}

func (s UserSubscription) groupName() string {
	if s.Group == nil {
		return ""
	}
	return strings.TrimSpace(s.Group.Name)
}

func (s UserSubscription) groupDailyLimit() float64 {
	if s.Group == nil {
		return 0
	}
	return s.Group.DailyLimitUSD.Float64()
}

func (s UserSubscription) groupWeeklyLimit() float64 {
	if s.Group == nil {
		return 0
	}
	return s.Group.WeeklyLimitUSD.Float64()
}

func (s UserSubscription) groupMonthlyLimit() float64 {
	if s.Group == nil {
		return 0
	}
	return s.Group.MonthlyLimitUSD.Float64()
}

func routesForGroup(group *Group) []string {
	if group == nil || strings.TrimSpace(group.Platform) == "" {
		return nil
	}
	return []string{strings.TrimSpace(group.Platform)}
}

func (s *Service) commitAuthIfCurrent(authGen uint64, base, email, password string, remember bool, auth AuthResponse) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return ErrAuthRequired
	}
	s.mu.Unlock()

	if remember && s.Secrets != nil {
		if err := s.saveAuth(base, email, password, auth); err != nil {
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
	s.email = email
	s.rememberLogin = remember
	s.memToken = auth.AccessToken
	s.refreshToken = auth.RefreshToken
	s.expiresAt = expiresAt(auth.ExpiresIn)
	return nil
}

func (s *Service) saveRefreshedAuthIfCurrent(authGen uint64, base string, auth RefreshResponse) bool {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	exp := expiresAt(auth.ExpiresIn)
	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return false
	}
	s.memToken = auth.AccessToken
	s.refreshToken = firstNonEmpty(auth.RefreshToken, s.refreshToken)
	s.expiresAt = exp
	remember := s.rememberLogin
	s.mu.Unlock()

	if remember && s.Secrets != nil {
		_ = s.Secrets.Update(map[string]string{
			tokenKey(base):     auth.AccessToken,
			refreshKey(base):   firstNonEmpty(auth.RefreshToken, s.savedRefreshToken(base)),
			expiresAtKey(base): strconv.FormatInt(exp.Unix(), 10),
		})
	}
	return s.authGenerationIs(authGen)
}

func (s *Service) saveLoginAuthIfCurrent(authGen uint64, base, email, password string, auth AuthResponse) bool {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return false
	}
	s.memToken = auth.AccessToken
	s.refreshToken = auth.RefreshToken
	s.expiresAt = expiresAt(auth.ExpiresIn)
	remember := s.rememberLogin
	s.mu.Unlock()

	if remember && s.Secrets != nil {
		_ = s.saveAuth(base, email, password, auth)
	}
	return s.authGenerationIs(authGen)
}

func (s *Service) loadTokenForGeneration(base string, authGen uint64) string {
	s.mu.Lock()
	if s.memToken != "" && !tokenExpired(s.expiresAt) {
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
	if token == "" || tokenExpired(s.savedExpiresAt(base)) {
		return ""
	}
	s.mu.Lock()
	if s.authGen != authGen {
		s.mu.Unlock()
		return ""
	}
	s.memToken = token
	s.expiresAt = s.savedExpiresAt(base)
	s.mu.Unlock()
	return token
}

func (s *Service) currentRefreshToken(base string) string {
	s.mu.Lock()
	refresh := s.refreshToken
	remember := s.rememberLogin
	s.mu.Unlock()
	if refresh != "" || !remember || base == "" {
		return refresh
	}
	refresh = s.savedRefreshToken(base)
	if refresh == "" {
		return ""
	}
	s.mu.Lock()
	s.refreshToken = refresh
	s.mu.Unlock()
	return refresh
}

func (s *Service) savedCredentials(base string) (string, string, bool) {
	s.mu.Lock()
	remember := s.rememberLogin
	email := strings.TrimSpace(s.email)
	s.mu.Unlock()
	if !remember || base == "" {
		return "", "", false
	}
	if saved := s.savedEmail(base); saved != "" {
		email = saved
	}
	password := s.savedPassword(base)
	return email, password, email != "" && password != ""
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
		s.persistMu.Lock()
		_ = s.Secrets.Update(map[string]string{emailKey(base): email})
		s.persistMu.Unlock()
	}
	return s.authGenerationIs(authGen)
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
	s.refreshToken = ""
	s.expiresAt = time.Time{}
	s.mu.Unlock()
	if base != "" {
		_ = s.clearSaved(base)
	}
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
	base = cfg.Sub2BaseURL
	if base != "" {
		if normalized, err := NormalizeBaseURL(base); err == nil {
			base = normalized
		}
	}
	return base, authGen
}

func (s *Service) clearSaved(base string) error {
	if s.Secrets == nil || base == "" {
		return nil
	}
	return s.Secrets.Update(map[string]string{
		tokenKey(base):     "",
		refreshKey(base):   "",
		expiresAtKey(base): "",
		emailKey(base):     "",
		passwordKey(base):  "",
	})
}

func (s *Service) saveAuth(base, email, password string, auth AuthResponse) error {
	if s.Secrets == nil || base == "" {
		return nil
	}
	return s.Secrets.Update(map[string]string{
		tokenKey(base):     auth.AccessToken,
		refreshKey(base):   auth.RefreshToken,
		expiresAtKey(base): strconv.FormatInt(expiresAt(auth.ExpiresIn).Unix(), 10),
		emailKey(base):     strings.TrimSpace(email),
		passwordKey(base):  password,
	})
}

func (s *Service) savedToken(base string) string {
	if s.Secrets == nil || base == "" {
		return ""
	}
	token, err := s.Secrets.Get(tokenKey(base))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func (s *Service) savedRefreshToken(base string) string {
	if s.Secrets == nil || base == "" {
		return ""
	}
	token, err := s.Secrets.Get(refreshKey(base))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func (s *Service) savedExpiresAt(base string) time.Time {
	if s.Secrets == nil || base == "" {
		return time.Time{}
	}
	raw, err := s.Secrets.Get(expiresAtKey(base))
	if err != nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func (s *Service) savedEmail(base string) string {
	if s.Secrets == nil || base == "" {
		return ""
	}
	email, err := s.Secrets.Get(emailKey(base))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(email)
}

func (s *Service) savedPassword(base string) string {
	if s.Secrets == nil || base == "" {
		return ""
	}
	password, err := s.Secrets.Get(passwordKey(base))
	if err != nil {
		return ""
	}
	return password
}

func tokenKey(base string) string {
	return "sub2:" + baseHash(base) + ":token"
}

func refreshKey(base string) string {
	return "sub2:" + baseHash(base) + ":refresh"
}

func expiresAtKey(base string) string {
	return "sub2:" + baseHash(base) + ":expires_at"
}

func emailKey(base string) string {
	return "sub2:" + baseHash(base) + ":email"
}

func passwordKey(base string) string {
	return "sub2:" + baseHash(base) + ":password"
}

func expiresAt(expiresIn int64) time.Time {
	if expiresIn <= 0 {
		return time.Now().Add(time.Hour)
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

func tokenExpired(expiresAt time.Time) bool {
	return expiresAt.IsZero() || time.Now().Add(30*time.Second).After(expiresAt)
}

func datePart(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}

func daysUntil(raw string, now time.Time) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, false
	}
	return int(t.Sub(now).Hours()/24) + 1, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveInt(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
