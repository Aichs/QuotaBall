package wailsui

import (
	"fmt"
	"math"
	"time"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

type AppStateDTO struct {
	Config   PublicConfigDTO `json:"config"`
	Snapshot SnapshotDTO     `json:"snapshot"`
}

type LoginRequest struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl"`
	RememberLogin bool   `json:"rememberLogin"`
}

type NewAPIOAuthStartRequest struct {
	BaseURL       string `json:"baseUrl"`
	RememberLogin bool   `json:"rememberLogin"`
	AutoCallback  bool   `json:"autoCallback"`
}

type NewAPIOAuthStartDTO struct {
	BaseURL      string `json:"baseUrl"`
	AuthorizeURL string `json:"authorizeUrl"`
	AutoCapture  bool   `json:"autoCapture"`
}

type NewAPIOAuthCompleteRequest struct {
	BaseURL        string `json:"baseUrl"`
	CallbackURL    string `json:"callbackUrl"`
	SessionCookies string `json:"sessionCookies,omitempty"`
	AccessToken    string `json:"accessToken,omitempty"`
	UserID         string `json:"userId,omitempty"`
	RememberLogin  bool   `json:"rememberLogin"`
}

type SettingsRequest struct {
	RefreshSec            int    `json:"refreshSec"`
	OnTop                 bool   `json:"onTop"`
	GlassEnabled          bool   `json:"glassEnabled"`
	RememberLogin         bool   `json:"rememberLogin"`
	Provider              string `json:"provider"`
	NewAPIBaseURL         string `json:"newapiBaseUrl"`
	Sub2BaseURL           string `json:"sub2BaseUrl"`
	CodexFastProxyEnabled bool   `json:"codexFastProxyEnabled"`
}

type PublicConfigDTO struct {
	Email                 string  `json:"email"`
	Provider              string  `json:"provider"`
	NewAPIBaseURL         string  `json:"newapiBaseUrl"`
	Sub2BaseURL           string  `json:"sub2BaseUrl"`
	Sub2Email             string  `json:"sub2Email"`
	RememberLogin         bool    `json:"rememberLogin"`
	RefreshSec            int     `json:"refreshSec"`
	Opacity               float64 `json:"opacity"`
	OnTop                 bool    `json:"onTop"`
	Theme                 string  `json:"theme"`
	WindowX               *int    `json:"windowX,omitempty"`
	WindowY               *int    `json:"windowY,omitempty"`
	GlassX                *int    `json:"glassX,omitempty"`
	GlassY                *int    `json:"glassY,omitempty"`
	GlassEnabled          bool    `json:"glassEnabled"`
	GlassMetric           string  `json:"glassMetric"`
	HasSavedLogin         bool    `json:"hasSavedLogin"`
	CodexFastProxyEnabled bool    `json:"codexFastProxyEnabled"`
}

type SnapshotDTO struct {
	Provider        string            `json:"provider"`
	Spend           float64           `json:"spend"`
	Wallet          float64           `json:"wallet"`
	Req             string            `json:"req"`
	Success         int               `json:"success"`
	Fail            int               `json:"fail"`
	Cache           string            `json:"cache"`
	Summary         SummaryDTO        `json:"summary"`
	Subscriptions   []SubscriptionDTO `json:"subscriptions"`
	Time            string            `json:"time"`
	TimeLabel       string            `json:"timeLabel"`
	Err             string            `json:"err"`
	OK              bool              `json:"ok"`
	LoggedIn        bool              `json:"loggedIn"`
	Email           string            `json:"email"`
	Loading         bool              `json:"loading"`
	RemainingDaily  float64           `json:"remainingDaily"`
	RemainingWeekly float64           `json:"remainingWeekly"`
}

type SummaryDTO struct {
	TotalUsedUSD                 float64 `json:"totalUsedUsd"`
	TotalDailyQuotaUSD           float64 `json:"totalDailyQuotaUsd"`
	TotalDailyUsedUSD            float64 `json:"totalDailyUsedUsd"`
	TotalForwardedRemainingUSD   float64 `json:"totalForwardedRemainingUsd"`
	TotalForwardedLimitUSD       float64 `json:"totalForwardedLimitUsd"`
	TotalForwardedUsedUSD        float64 `json:"totalForwardedUsedUsd"`
	TotalRemainingUSD            float64 `json:"totalRemainingUsd"`
	TotalDailyRemainingUSD       float64 `json:"totalDailyRemainingUsd"`
	TotalDailyForwardedQuotaUSD  float64 `json:"totalDailyForwardedQuotaUsd"`
	TotalDailyForwardedUsedUSD   float64 `json:"totalDailyForwardedUsedUsd"`
	TotalDailyForwardedRemainUSD float64 `json:"totalDailyForwardedRemainUsd"`
	TotalWeeklyQuotaUSD          float64 `json:"totalWeeklyQuotaUsd"`
	TotalWeeklyUsedUSD           float64 `json:"totalWeeklyUsedUsd"`
	TotalWeeklyRemainingUSD      float64 `json:"totalWeeklyRemainingUsd"`
	TotalMonthlyQuotaUSD         float64 `json:"totalMonthlyQuotaUsd"`
	TotalMonthlyUsedUSD          float64 `json:"totalMonthlyUsedUsd"`
	TotalMonthlyRemainingUSD     float64 `json:"totalMonthlyRemainingUsd"`
}

type SubscriptionDTO struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	DaysLeft           string   `json:"daysLeft"`
	DaysLeftText       string   `json:"daysLeftText"`
	Start              string   `json:"start"`
	End                string   `json:"end"`
	Routes             []string `json:"routes"`
	DailyLimit         float64  `json:"dailyLimit"`
	DailyUsed          float64  `json:"dailyUsed"`
	DailyRemaining     float64  `json:"dailyRemaining"`
	DailyPercent       float64  `json:"dailyPercent"`
	ForwardedLimit     float64  `json:"forwardedLimit"`
	ForwardedUsed      float64  `json:"forwardedUsed"`
	ForwardedRemaining float64  `json:"forwardedRemaining"`
	ForwardedPercent   float64  `json:"forwardedPercent"`
	WeeklyLimit        float64  `json:"weeklyLimit"`
	WeeklyUsed         float64  `json:"weeklyUsed"`
	WeeklyRemaining    float64  `json:"weeklyRemaining"`
	WeeklyPercent      float64  `json:"weeklyPercent"`
	MonthlyLimit       float64  `json:"monthlyLimit"`
	MonthlyUsed        float64  `json:"monthlyUsed"`
	MonthlyRemaining   float64  `json:"monthlyRemaining"`
	MonthlyPercent     float64  `json:"monthlyPercent"`
}

func configDTO(cfg config.Config, hasSavedLogin bool) PublicConfigDTO {
	cfg.Normalize()
	return PublicConfigDTO{
		Email:                 cfg.Email,
		Provider:              cfg.Provider,
		NewAPIBaseURL:         cfg.NewAPIBaseURL,
		Sub2BaseURL:           cfg.Sub2BaseURL,
		Sub2Email:             cfg.Sub2Email,
		RememberLogin:         cfg.RememberLogin,
		RefreshSec:            cfg.RefreshSec,
		Opacity:               cfg.Opacity,
		OnTop:                 cfg.OnTop,
		Theme:                 cfg.Theme,
		WindowX:               cloneInt(cfg.WX),
		WindowY:               cloneInt(cfg.WY),
		GlassX:                cloneInt(cfg.TbarX),
		GlassY:                cloneInt(cfg.TbarY),
		GlassEnabled:          cfg.TbarEnabled,
		GlassMetric:           cfg.TbarMetric,
		HasSavedLogin:         hasSavedLogin,
		CodexFastProxyEnabled: cfg.CodexFastProxyEnabled,
	}
}

func snapshotDTO(s krill.Snapshot) SnapshotDTO {
	subs := make([]SubscriptionDTO, 0, len(s.Subscriptions))
	for _, sub := range s.Subscriptions {
		subs = append(subs, subscriptionDTO(s.Provider, sub))
	}
	return SnapshotDTO{
		Provider:        s.Provider,
		Spend:           s.Spend,
		Wallet:          s.Wallet,
		Req:             fallback(s.Req, "-"),
		Success:         s.Success,
		Fail:            s.Fail,
		Cache:           fallback(s.Cache, "-"),
		Summary:         summaryDTO(s.Provider, s.Summary),
		Subscriptions:   subs,
		Time:            timeString(s.Time),
		TimeLabel:       timeLabel(s.Time),
		Err:             s.Err,
		OK:              s.OK,
		LoggedIn:        s.LoggedIn,
		Email:           s.Email,
		Loading:         s.Loading,
		RemainingDaily:  math.Max(0, s.RemainingDaily()),
		RemainingWeekly: math.Max(0, s.RemainingWeekly()),
	}
}

func summaryDTO(provider string, s krill.Summary) SummaryDTO {
	weeklyQuota := s.TotalWeeklyQuotaUSD
	if usesKrillQuotaFallbacks(provider) && weeklyQuota == 0 {
		weeklyQuota = s.TotalDailyQuotaUSD
	}
	weeklyRemaining := s.TotalWeeklyRemainingUSD
	if usesKrillQuotaFallbacks(provider) && weeklyRemaining == 0 && weeklyQuota > 0 {
		weeklyRemaining = firstPositiveFloat(s.TotalRemainingUSD, s.TotalDailyRemainingUSD, math.Max(0, weeklyQuota-s.TotalUsedUSD))
	}
	dailyUsed := s.TotalDailyUsedUSD
	if usesKrillQuotaFallbacks(provider) && dailyUsed == 0 && s.TotalDailyQuotaUSD > 0 {
		dailyUsed = math.Max(0, s.TotalDailyQuotaUSD-s.TotalDailyRemainingUSD)
	}
	weeklyUsed := s.TotalWeeklyUsedUSD
	if usesKrillQuotaFallbacks(provider) && weeklyUsed == 0 && weeklyQuota > 0 {
		weeklyUsed = math.Max(0, weeklyQuota-weeklyRemaining)
	}
	monthlyUsed := s.TotalMonthlyUsedUSD
	if usesKrillQuotaFallbacks(provider) && monthlyUsed == 0 {
		monthlyUsed = s.TotalUsedUSD
	}
	monthlyQuota := s.TotalMonthlyQuotaUSD
	monthlyRemaining := s.TotalMonthlyRemainingUSD
	if usesKrillQuotaFallbacks(provider) && monthlyRemaining == 0 && monthlyQuota > 0 {
		monthlyRemaining = math.Max(0, monthlyQuota-monthlyUsed)
	}
	return SummaryDTO{
		TotalUsedUSD:                 s.TotalUsedUSD,
		TotalDailyQuotaUSD:           s.TotalDailyQuotaUSD,
		TotalDailyUsedUSD:            dailyUsed,
		TotalForwardedRemainingUSD:   s.TotalForwardedRemainingUSD,
		TotalForwardedLimitUSD:       s.TotalForwardedLimitUSD,
		TotalForwardedUsedUSD:        s.TotalForwardedUsedUSD,
		TotalRemainingUSD:            s.TotalRemainingUSD,
		TotalDailyRemainingUSD:       s.TotalDailyRemainingUSD,
		TotalDailyForwardedQuotaUSD:  s.TotalDailyForwardedQuotaUSD,
		TotalDailyForwardedUsedUSD:   s.TotalDailyForwardedUsedUSD,
		TotalDailyForwardedRemainUSD: s.TotalDailyForwardedRemainUSD,
		TotalWeeklyQuotaUSD:          weeklyQuota,
		TotalWeeklyUsedUSD:           weeklyUsed,
		TotalWeeklyRemainingUSD:      weeklyRemaining,
		TotalMonthlyQuotaUSD:         monthlyQuota,
		TotalMonthlyUsedUSD:          monthlyUsed,
		TotalMonthlyRemainingUSD:     monthlyRemaining,
	}
}

func subscriptionDTO(provider string, s krill.Subscription) SubscriptionDTO {
	days := fmt.Sprint(s.DaysLeft)
	if days == "" || days == "<nil>" {
		days = "?"
	}
	weeklyLimit := s.WeeklyLimit
	if usesKrillQuotaFallbacks(provider) && weeklyLimit == 0 {
		weeklyLimit = s.DailyLimit
	}
	weeklyUsed := s.WeeklyUsed
	if usesKrillQuotaFallbacks(provider) && weeklyUsed == 0 {
		weeklyUsed = s.DailyUsed
	}
	weeklyRemaining := s.WeeklyRemaining
	if usesKrillQuotaFallbacks(provider) && weeklyRemaining == 0 {
		weeklyRemaining = s.DailyRemaining
	}
	weeklyPercent := s.WeeklyPercent
	if usesKrillQuotaFallbacks(provider) && weeklyPercent == 0 {
		weeklyPercent = s.DailyPercent
	}
	monthlyLimit := s.MonthlyLimit
	if usesKrillQuotaFallbacks(provider) && monthlyLimit == 0 {
		monthlyLimit = weeklyLimit
	}
	monthlyUsed := s.MonthlyUsed
	if usesKrillQuotaFallbacks(provider) && monthlyUsed == 0 {
		monthlyUsed = weeklyUsed
	}
	monthlyRemaining := s.MonthlyRemaining
	if usesKrillQuotaFallbacks(provider) && monthlyRemaining == 0 && monthlyLimit > 0 {
		monthlyRemaining = math.Max(0, monthlyLimit-monthlyUsed)
	}
	monthlyPercent := s.MonthlyPercent
	if usesKrillQuotaFallbacks(provider) && monthlyPercent == 0 && monthlyLimit > 0 {
		monthlyPercent = math.Max(0, math.Min(100, math.Round((monthlyUsed/monthlyLimit*100)*10)/10))
	}
	return SubscriptionDTO{
		ID:                 s.ID,
		Name:               fallback(s.Name, "套餐"),
		DaysLeft:           days,
		DaysLeftText:       days + " 天后到期",
		Start:              s.Start,
		End:                s.End,
		Routes:             append([]string(nil), s.Routes...),
		DailyLimit:         s.DailyLimit,
		DailyUsed:          s.DailyUsed,
		DailyRemaining:     s.DailyRemaining,
		DailyPercent:       s.DailyPercent,
		ForwardedLimit:     s.ForwardedLimit,
		ForwardedUsed:      s.ForwardedUsed,
		ForwardedRemaining: s.ForwardedRemaining,
		ForwardedPercent:   s.ForwardedPercent,
		WeeklyLimit:        weeklyLimit,
		WeeklyUsed:         weeklyUsed,
		WeeklyRemaining:    weeklyRemaining,
		WeeklyPercent:      weeklyPercent,
		MonthlyLimit:       monthlyLimit,
		MonthlyUsed:        monthlyUsed,
		MonthlyRemaining:   monthlyRemaining,
		MonthlyPercent:     monthlyPercent,
	}
}

func usesKrillQuotaFallbacks(provider string) bool {
	return provider == "" || provider == config.ProviderKrill
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func cloneInt(v *int) *int {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func fallback(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func timeLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04")
}
