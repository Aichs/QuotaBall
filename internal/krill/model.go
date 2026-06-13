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
	Provider      string
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

func (s Snapshot) RemainingWeekly() float64 {
	if s.Summary.TotalWeeklyRemainingUSD > 0 {
		return math.Max(0, s.Summary.TotalWeeklyRemainingUSD)
	}
	if s.Summary.TotalWeeklyQuotaUSD > 0 {
		return math.Max(0, s.Summary.TotalWeeklyQuotaUSD-s.Spend)
	}
	return s.RemainingDaily()
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
	TotalWeeklyQuotaUSD          float64 `json:"-"`
	TotalWeeklyRemainingUSD      float64 `json:"-"`
	TotalMonthlyQuotaUSD         float64 `json:"-"`
	TotalMonthlyUsedUSD          float64 `json:"-"`
	TotalMonthlyRemainingUSD     float64 `json:"-"`
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
	WeeklyLimit        float64
	WeeklyUsed         float64
	WeeklyRemaining    float64
	WeeklyPercent      float64
	MonthlyLimit       float64
	MonthlyUsed        float64
	MonthlyRemaining   float64
	MonthlyPercent     float64
}

type SubscriptionPayload struct {
	Summary            Summary              `json:"summary"`
	Subscriptions      []SubscriptionRecord `json:"subscriptions"`
	CreditBalanceUSD   float64              `json:"credit_balance_usd"`
	WelfareBalanceUSD  float64              `json:"welfare_balance_usd"`
	AdditionalRawProps map[string]any       `json:"-"`
}

type looseFloat float64

type looseInt int

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

func (i *looseInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*i = 0
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*i = 0
			return nil
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		*i = looseInt(v)
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*i = looseInt(n)
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
		TotalWeeklyQuotaUSD:          float64(raw.TotalDailyQuotaUSD),
		TotalWeeklyRemainingUSD:      firstPositive(float64(raw.TotalRemainingUSD), float64(raw.TotalDailyRemainingUSD), math.Max(0, float64(raw.TotalDailyQuotaUSD)-float64(raw.TotalUsedUSD))),
		TotalMonthlyUsedUSD:          float64(raw.TotalUsedUSD),
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
	SubscriptionID      string  `json:"subscription_id"`
	SubscriptionStartAt string  `json:"subscription_start_at"`
	SubscriptionEndAt   string  `json:"subscription_end_at"`
	TotalUsedUSD        float64 `json:"total_used_usd"`
	Plan                Plan    `json:"plan"`
	Quota               Quota   `json:"quota"`
}

func (r *SubscriptionRecord) UnmarshalJSON(data []byte) error {
	var raw struct {
		SubscriptionID      looseString `json:"subscription_id"`
		SubscriptionStartAt string      `json:"subscription_start_at"`
		SubscriptionEndAt   string      `json:"subscription_end_at"`
		TotalUsedUSD        looseFloat  `json:"total_used_usd"`
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
		TotalUsedUSD:        float64(raw.TotalUsedUSD),
		Plan:                raw.Plan,
		Quota:               raw.Quota,
	}
	return nil
}

type Plan struct {
	Name           string
	EntryRouteKeys []string
	BillingType    string
	DurationDays   int
	TotalCredits   float64
}

func (p *Plan) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name           string     `json:"name"`
		EntryRouteKeys []string   `json:"entry_route_keys"`
		BillingType    string     `json:"billing_type"`
		DurationDays   looseInt   `json:"duration_days"`
		TotalCredits   looseFloat `json:"total_credits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = Plan{
		Name:           raw.Name,
		EntryRouteKeys: append([]string(nil), raw.EntryRouteKeys...),
		BillingType:    raw.BillingType,
		DurationDays:   int(raw.DurationDays),
		TotalCredits:   float64(raw.TotalCredits),
	}
	return nil
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
	summary := p.Summary
	var monthlyQuota float64
	for _, rec := range p.Subscriptions {
		sub := rec.ToSubscription(now)
		monthlyQuota += sub.MonthlyLimit
		subs = append(subs, sub)
	}
	if summary.TotalWeeklyQuotaUSD == 0 {
		summary.TotalWeeklyQuotaUSD = summary.TotalDailyQuotaUSD
	}
	if summary.TotalWeeklyRemainingUSD == 0 && summary.TotalWeeklyQuotaUSD > 0 {
		summary.TotalWeeklyRemainingUSD = firstPositive(summary.TotalRemainingUSD, summary.TotalDailyRemainingUSD, math.Max(0, summary.TotalWeeklyQuotaUSD-summary.TotalUsedUSD))
	}
	if monthlyQuota > 0 {
		summary.TotalMonthlyQuotaUSD = monthlyQuota
	}
	if summary.TotalMonthlyUsedUSD == 0 {
		summary.TotalMonthlyUsedUSD = summary.TotalUsedUSD
	}
	if summary.TotalMonthlyQuotaUSD > 0 {
		summary.TotalMonthlyRemainingUSD = math.Max(0, summary.TotalMonthlyQuotaUSD-summary.TotalMonthlyUsedUSD)
	}
	return Snapshot{
		Spend:         p.Summary.TotalUsedUSD,
		Wallet:        p.CreditBalanceUSD + p.WelfareBalanceUSD,
		Req:           "-",
		Cache:         "-",
		Summary:       summary,
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

	weeklyLimit := r.Quota.DailyLimitUSD
	weeklyUsed := r.Quota.UsedUSD
	weeklyRemaining := r.Quota.RemainingUSD
	monthlyLimit := monthlyQuotaLimit(r.Plan, weeklyLimit)
	monthlyUsed := r.TotalUsedUSD
	if monthlyUsed == 0 {
		monthlyUsed = weeklyUsed
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
		WeeklyLimit:        weeklyLimit,
		WeeklyUsed:         weeklyUsed,
		WeeklyRemaining:    weeklyRemaining,
		WeeklyPercent:      percent(weeklyUsed, weeklyLimit),
		MonthlyLimit:       monthlyLimit,
		MonthlyUsed:        monthlyUsed,
		MonthlyRemaining:   math.Max(0, monthlyLimit-monthlyUsed),
		MonthlyPercent:     percent(monthlyUsed, monthlyLimit),
	}
}

func monthlyQuotaLimit(plan Plan, weeklyLimit float64) float64 {
	if plan.TotalCredits > 0 {
		return plan.TotalCredits
	}
	if weeklyLimit <= 0 {
		return 0
	}
	if plan.DurationDays <= 0 {
		return weeklyLimit
	}
	windows := math.Floor(float64(plan.DurationDays) / 7)
	if windows < 1 {
		windows = 1
	}
	return weeklyLimit * windows
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func percent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	v := math.Round((used/limit*100)*10) / 10
	return math.Max(0, math.Min(100, v))
}
