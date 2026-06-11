package krill

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

type Snapshot struct {
	Spend         float64
	Wallet        float64
	Req           string
	Success       int
	Fail          int
	Cache         string
	Summary       Summary
	Subscriptions []Subscription
	Time          time.Time
	Err           string
	OK            bool
	LoggedIn      bool
	Email         string
	Loading       bool
}

func EmptySnapshot(message string) Snapshot {
	return Snapshot{
		Req:      "-",
		Cache:    "-",
		Err:      message,
		OK:       false,
		LoggedIn: false,
	}
}

func (s Snapshot) RemainingDaily() float64 {
	return math.Max(0, s.Summary.TotalDailyQuotaUSD-s.Spend)
}

type Summary struct {
	TotalUsedUSD                 float64 `json:"total_used_usd"`
	TotalDailyQuotaUSD           float64 `json:"total_daily_quota_usd"`
	TotalForwardedRemainingUSD   float64 `json:"total_forwarded_remaining_usd"`
	TotalForwardedLimitUSD       float64 `json:"total_forwarded_limit_usd"`
	TotalForwardedUsedUSD        float64 `json:"total_forwarded_used_usd"`
	TotalRemainingUSD            float64 `json:"total_remaining_usd"`
	TotalDailyRemainingUSD       float64 `json:"total_daily_remaining_usd"`
	TotalDailyForwardedQuotaUSD  float64 `json:"total_daily_forwarded_quota_usd"`
	TotalDailyForwardedUsedUSD   float64 `json:"total_daily_forwarded_used_usd"`
	TotalDailyForwardedRemainUSD float64 `json:"total_daily_forwarded_remain_usd"`
}

type Subscription struct {
	ID                 string
	Name               string
	DaysLeft           any
	Start              string
	End                string
	Routes             []string
	DailyLimit         float64
	DailyUsed          float64
	DailyRemaining     float64
	DailyPercent       float64
	ForwardedLimit     float64
	ForwardedUsed      float64
	ForwardedRemaining float64
	ForwardedPercent   float64
}

type SubscriptionPayload struct {
	Summary            Summary              `json:"summary"`
	Subscriptions      []SubscriptionRecord `json:"subscriptions"`
	CreditBalanceUSD   float64              `json:"credit_balance_usd"`
	WelfareBalanceUSD  float64              `json:"welfare_balance_usd"`
	AdditionalRawProps map[string]any       `json:"-"`
}

type looseFloat float64

type looseString string

func (s *looseString) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*s = ""
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = looseString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*s = looseString(n.String())
		return nil
	}
	return json.Unmarshal(data, (*string)(s))
}

func (f *looseFloat) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = looseFloat(v)
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return err
	}
	*f = looseFloat(v)
	return nil
}

func (s *Summary) UnmarshalJSON(data []byte) error {
	var raw struct {
		TotalUsedUSD                 looseFloat `json:"total_used_usd"`
		TotalDailyQuotaUSD           looseFloat `json:"total_daily_quota_usd"`
		TotalForwardedRemainingUSD   looseFloat `json:"total_forwarded_remaining_usd"`
		TotalForwardedLimitUSD       looseFloat `json:"total_forwarded_limit_usd"`
		TotalForwardedUsedUSD        looseFloat `json:"total_forwarded_used_usd"`
		TotalRemainingUSD            looseFloat `json:"total_remaining_usd"`
		TotalDailyRemainingUSD       looseFloat `json:"total_daily_remaining_usd"`
		TotalDailyForwardedQuotaUSD  looseFloat `json:"total_daily_forwarded_quota_usd"`
		TotalDailyForwardedUsedUSD   looseFloat `json:"total_daily_forwarded_used_usd"`
		TotalDailyForwardedRemainUSD looseFloat `json:"total_daily_forwarded_remain_usd"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = Summary{
		TotalUsedUSD:                 float64(raw.TotalUsedUSD),
		TotalDailyQuotaUSD:           float64(raw.TotalDailyQuotaUSD),
		TotalForwardedRemainingUSD:   float64(raw.TotalForwardedRemainingUSD),
		TotalForwardedLimitUSD:       float64(raw.TotalForwardedLimitUSD),
		TotalForwardedUsedUSD:        float64(raw.TotalForwardedUsedUSD),
		TotalRemainingUSD:            float64(raw.TotalRemainingUSD),
		TotalDailyRemainingUSD:       float64(raw.TotalDailyRemainingUSD),
		TotalDailyForwardedQuotaUSD:  float64(raw.TotalDailyForwardedQuotaUSD),
		TotalDailyForwardedUsedUSD:   float64(raw.TotalDailyForwardedUsedUSD),
		TotalDailyForwardedRemainUSD: float64(raw.TotalDailyForwardedRemainUSD),
	}
	return nil
}

func (p *SubscriptionPayload) UnmarshalJSON(data []byte) error {
	var raw struct {
		Summary           Summary              `json:"summary"`
		Subscriptions     []SubscriptionRecord `json:"subscriptions"`
		CreditBalanceUSD  looseFloat           `json:"credit_balance_usd"`
		WelfareBalanceUSD looseFloat           `json:"welfare_balance_usd"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Summary = raw.Summary
	p.Subscriptions = raw.Subscriptions
	p.CreditBalanceUSD = float64(raw.CreditBalanceUSD)
	p.WelfareBalanceUSD = float64(raw.WelfareBalanceUSD)
	return nil
}

type SubscriptionRecord struct {
	SubscriptionID      string `json:"subscription_id"`
	SubscriptionStartAt string `json:"subscription_start_at"`
	SubscriptionEndAt   string `json:"subscription_end_at"`
	Plan                Plan   `json:"plan"`
	Quota               Quota  `json:"quota"`
}

func (r *SubscriptionRecord) UnmarshalJSON(data []byte) error {
	var raw struct {
		SubscriptionID      looseString `json:"subscription_id"`
		SubscriptionStartAt string      `json:"subscription_start_at"`
		SubscriptionEndAt   string      `json:"subscription_end_at"`
		Plan                Plan        `json:"plan"`
		Quota               Quota       `json:"quota"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = SubscriptionRecord{
		SubscriptionID:      string(raw.SubscriptionID),
		SubscriptionStartAt: raw.SubscriptionStartAt,
		SubscriptionEndAt:   raw.SubscriptionEndAt,
		Plan:                raw.Plan,
		Quota:               raw.Quota,
	}
	return nil
}

type Plan struct {
	Name           string   `json:"name"`
	EntryRouteKeys []string `json:"entry_route_keys"`
}

type Quota struct {
	DailyLimitUSD         float64 `json:"daily_limit_usd"`
	UsedUSD               float64 `json:"used_usd"`
	RemainingUSD          float64 `json:"remaining_usd"`
	ForwardedLimitUSD     float64 `json:"forwarded_limit_usd"`
	ForwardedUsedUSD      float64 `json:"forwarded_used_usd"`
	ForwardedRemainingUSD float64 `json:"forwarded_remaining_usd"`
}

func (q *Quota) UnmarshalJSON(data []byte) error {
	var raw struct {
		DailyLimitUSD         looseFloat `json:"daily_limit_usd"`
		UsedUSD               looseFloat `json:"used_usd"`
		RemainingUSD          looseFloat `json:"remaining_usd"`
		ForwardedLimitUSD     looseFloat `json:"forwarded_limit_usd"`
		ForwardedUsedUSD      looseFloat `json:"forwarded_used_usd"`
		ForwardedRemainingUSD looseFloat `json:"forwarded_remaining_usd"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = Quota{
		DailyLimitUSD:         float64(raw.DailyLimitUSD),
		UsedUSD:               float64(raw.UsedUSD),
		RemainingUSD:          float64(raw.RemainingUSD),
		ForwardedLimitUSD:     float64(raw.ForwardedLimitUSD),
		ForwardedUsedUSD:      float64(raw.ForwardedUsedUSD),
		ForwardedRemainingUSD: float64(raw.ForwardedRemainingUSD),
	}
	return nil
}

func (p SubscriptionPayload) ToSnapshot(now time.Time) Snapshot {
	subs := make([]Subscription, 0, len(p.Subscriptions))
	for _, rec := range p.Subscriptions {
		subs = append(subs, rec.ToSubscription(now))
	}
	return Snapshot{
		Spend:         p.Summary.TotalUsedUSD,
		Wallet:        p.CreditBalanceUSD + p.WelfareBalanceUSD,
		Req:           "-",
		Cache:         "-",
		Summary:       p.Summary,
		Subscriptions: subs,
		Time:          now,
		OK:            true,
		LoggedIn:      true,
	}
}

func (r SubscriptionRecord) ToSubscription(now time.Time) Subscription {
	endDate := r.SubscriptionEndAt
	daysLeft := any("?")
	if len(r.SubscriptionEndAt) >= 10 {
		endDate = r.SubscriptionEndAt[:10]
	}
	if end, err := time.Parse(time.RFC3339, r.SubscriptionEndAt); err == nil {
		daysLeft = int(end.Sub(now).Hours()/24) + 1
	}

	startDate := r.SubscriptionStartAt
	if len(startDate) >= 10 {
		startDate = startDate[:10]
	}

	return Subscription{
		ID:                 r.SubscriptionID,
		Name:               r.Plan.Name,
		DaysLeft:           daysLeft,
		Start:              startDate,
		End:                endDate,
		Routes:             append([]string(nil), r.Plan.EntryRouteKeys...),
		DailyLimit:         r.Quota.DailyLimitUSD,
		DailyUsed:          r.Quota.UsedUSD,
		DailyRemaining:     r.Quota.RemainingUSD,
		DailyPercent:       percent(r.Quota.UsedUSD, r.Quota.DailyLimitUSD),
		ForwardedLimit:     r.Quota.ForwardedLimitUSD,
		ForwardedUsed:      r.Quota.ForwardedUsedUSD,
		ForwardedRemaining: r.Quota.ForwardedRemainingUSD,
		ForwardedPercent:   percent(r.Quota.ForwardedUsedUSD, r.Quota.ForwardedLimitUSD),
	}
}

func percent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	v := math.Round((used/limit*100)*10) / 10
	return math.Max(0, math.Min(100, v))
}
