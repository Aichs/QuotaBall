package wailsui

import (
	"fmt"
	"math"
	"time"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
)

type AppStateDTO struct {
	Config   PublicConfigDTO `json:"config"`
	Snapshot SnapshotDTO     `json:"snapshot"`
}

type LoginRequest struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	RememberLogin bool   `json:"rememberLogin"`
}

type SettingsRequest struct {
	RefreshSec    int  `json:"refreshSec"`
	OnTop         bool `json:"onTop"`
	GlassEnabled  bool `json:"glassEnabled"`
	RememberLogin bool `json:"rememberLogin"`
}

type PublicConfigDTO struct {
	Email         string  `json:"email"`
	RememberLogin bool    `json:"rememberLogin"`
	RefreshSec    int     `json:"refreshSec"`
	Opacity       float64 `json:"opacity"`
	OnTop         bool    `json:"onTop"`
	Theme         string  `json:"theme"`
	WindowX       *int    `json:"windowX,omitempty"`
	WindowY       *int    `json:"windowY,omitempty"`
	GlassX        *int    `json:"glassX,omitempty"`
	GlassY        *int    `json:"glassY,omitempty"`
	GlassEnabled  bool    `json:"glassEnabled"`
	GlassMetric   string  `json:"glassMetric"`
	HasSavedLogin bool    `json:"hasSavedLogin"`
}

type SnapshotDTO struct {
	Spend          float64           `json:"spend"`
	Wallet         float64           `json:"wallet"`
	Req            string            `json:"req"`
	Success        int               `json:"success"`
	Fail           int               `json:"fail"`
	Cache          string            `json:"cache"`
	Summary        SummaryDTO        `json:"summary"`
	Subscriptions  []SubscriptionDTO `json:"subscriptions"`
	Time           string            `json:"time"`
	TimeLabel      string            `json:"timeLabel"`
	Err            string            `json:"err"`
	OK             bool              `json:"ok"`
	LoggedIn       bool              `json:"loggedIn"`
	Email          string            `json:"email"`
	Loading        bool              `json:"loading"`
	RemainingDaily float64           `json:"remainingDaily"`
}

type SummaryDTO struct {
	TotalUsedUSD                 float64 `json:"totalUsedUsd"`
	TotalDailyQuotaUSD           float64 `json:"totalDailyQuotaUsd"`
	TotalForwardedRemainingUSD   float64 `json:"totalForwardedRemainingUsd"`
	TotalForwardedLimitUSD       float64 `json:"totalForwardedLimitUsd"`
	TotalForwardedUsedUSD        float64 `json:"totalForwardedUsedUsd"`
	TotalRemainingUSD            float64 `json:"totalRemainingUsd"`
	TotalDailyRemainingUSD       float64 `json:"totalDailyRemainingUsd"`
	TotalDailyForwardedQuotaUSD  float64 `json:"totalDailyForwardedQuotaUsd"`
	TotalDailyForwardedUsedUSD   float64 `json:"totalDailyForwardedUsedUsd"`
	TotalDailyForwardedRemainUSD float64 `json:"totalDailyForwardedRemainUsd"`
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
}

func configDTO(cfg config.Config, hasSavedLogin bool) PublicConfigDTO {
	cfg.Normalize()
	return PublicConfigDTO{
		Email:         cfg.Email,
		RememberLogin: cfg.RememberLogin,
		RefreshSec:    cfg.RefreshSec,
		Opacity:       cfg.Opacity,
		OnTop:         cfg.OnTop,
		Theme:         cfg.Theme,
		WindowX:       cloneInt(cfg.WX),
		WindowY:       cloneInt(cfg.WY),
		GlassX:        cloneInt(cfg.TbarX),
		GlassY:        cloneInt(cfg.TbarY),
		GlassEnabled:  cfg.TbarEnabled,
		GlassMetric:   cfg.TbarMetric,
		HasSavedLogin: hasSavedLogin,
	}
}

func snapshotDTO(s krill.Snapshot) SnapshotDTO {
	subs := make([]SubscriptionDTO, 0, len(s.Subscriptions))
	for _, sub := range s.Subscriptions {
		subs = append(subs, subscriptionDTO(sub))
	}
	return SnapshotDTO{
		Spend:          s.Spend,
		Wallet:         s.Wallet,
		Req:            fallback(s.Req, "-"),
		Success:        s.Success,
		Fail:           s.Fail,
		Cache:          fallback(s.Cache, "-"),
		Summary:        summaryDTO(s.Summary),
		Subscriptions:  subs,
		Time:           timeString(s.Time),
		TimeLabel:      timeLabel(s.Time),
		Err:            s.Err,
		OK:             s.OK,
		LoggedIn:       s.LoggedIn,
		Email:          s.Email,
		Loading:        s.Loading,
		RemainingDaily: math.Max(0, s.RemainingDaily()),
	}
}

func summaryDTO(s krill.Summary) SummaryDTO {
	return SummaryDTO{
		TotalUsedUSD:                 s.TotalUsedUSD,
		TotalDailyQuotaUSD:           s.TotalDailyQuotaUSD,
		TotalForwardedRemainingUSD:   s.TotalForwardedRemainingUSD,
		TotalForwardedLimitUSD:       s.TotalForwardedLimitUSD,
		TotalForwardedUsedUSD:        s.TotalForwardedUsedUSD,
		TotalRemainingUSD:            s.TotalRemainingUSD,
		TotalDailyRemainingUSD:       s.TotalDailyRemainingUSD,
		TotalDailyForwardedQuotaUSD:  s.TotalDailyForwardedQuotaUSD,
		TotalDailyForwardedUsedUSD:   s.TotalDailyForwardedUsedUSD,
		TotalDailyForwardedRemainUSD: s.TotalDailyForwardedRemainUSD,
	}
}

func subscriptionDTO(s krill.Subscription) SubscriptionDTO {
	days := fmt.Sprint(s.DaysLeft)
	if days == "" || days == "<nil>" {
		days = "?"
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
	}
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
