//go:build windows

package ui

import (
	"fmt"
	"math"

	"github.com/lxn/walk"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

type panelStatCard struct {
	title  string
	value  string
	sub    string
	accent walk.Color
}

func panelStatCards(s krill.Snapshot) []panelStatCard {
	if s.Provider == config.ProviderNewAPI {
		return []panelStatCard{
			{"当前余额", panelMoney(s.Wallet, 2), "可用余额", walk.RGB(40, 184, 255)},
			{"历史消耗", panelMoney(s.Spend, 2), "累计已用额度", walk.RGB(255, 173, 47)},
			{"请求次数", panelNonEmpty(s.Req, "-"), "累计请求数", walk.RGB(49, 223, 154)},
		}
	}
	if s.Provider == config.ProviderSub2 {
		dailyQuota := s.Summary.TotalDailyQuotaUSD
		dailyRemaining := s.Summary.TotalDailyRemainingUSD
		dailyUsed := firstPositive(s.Summary.TotalDailyUsedUSD, math.Max(0, dailyQuota-dailyRemaining))
		weeklyQuota := s.Summary.TotalWeeklyQuotaUSD
		weeklyRemaining := s.Summary.TotalWeeklyRemainingUSD
		weeklyUsed := firstPositive(s.Summary.TotalWeeklyUsedUSD, math.Max(0, weeklyQuota-weeklyRemaining))
		monthlyQuota := s.Summary.TotalMonthlyQuotaUSD
		monthlyRemaining := s.Summary.TotalMonthlyRemainingUSD
		monthlyUsed := firstPositive(s.Summary.TotalMonthlyUsedUSD, math.Max(0, monthlyQuota-monthlyRemaining))
		return []panelStatCard{
			{"账户余额", panelMoney(s.Wallet, 2), "Sub2 账户余额", walk.RGB(40, 184, 255)},
			{"今日剩余", panelMoney(dailyRemaining, 2), fmt.Sprintf("已用 %s / 总计 %s", panelMoney(dailyUsed, 2), panelMoney(dailyQuota, 2)), walk.RGB(255, 173, 47)},
			{"本周剩余", panelMoney(weeklyRemaining, 2), fmt.Sprintf("已用 %s / 总计 %s", panelMoney(weeklyUsed, 2), panelMoney(weeklyQuota, 2)), walk.RGB(49, 223, 154)},
			{"本月剩余", panelMoney(monthlyRemaining, 2), fmt.Sprintf("已用 %s / 总计 %s", panelMoney(monthlyUsed, 2), panelMoney(monthlyQuota, 2)), walk.RGB(255, 127, 138)},
		}
	}

	weeklyQuota := firstPositive(s.Summary.TotalWeeklyQuotaUSD, s.Summary.TotalDailyQuotaUSD)
	weeklyRemaining := firstPositive(s.Summary.TotalWeeklyRemainingUSD, s.Summary.TotalRemainingUSD, math.Max(0, weeklyQuota-s.Spend))
	weeklyUsed := math.Max(0, weeklyQuota-weeklyRemaining)
	monthlyQuota := s.Summary.TotalMonthlyQuotaUSD
	monthlyUsed := firstPositive(s.Summary.TotalMonthlyUsedUSD, s.Summary.TotalUsedUSD, s.Spend)
	monthlyValue := "-"
	monthlySub := fmt.Sprintf("已用 %s", panelMoney(monthlyUsed, 2))
	if monthlyQuota > 0 {
		monthlyValue = panelMoney(monthlyQuota, 0)
		monthlySub = fmt.Sprintf("已用 %s / 总计 %s", panelMoney(monthlyUsed, 2), panelMoney(monthlyQuota, 0))
	}
	return []panelStatCard{
		{"本周剩余", panelMoney(weeklyRemaining, 2), fmt.Sprintf("已用 %s / 总计 %s", panelMoney(weeklyUsed, 2), panelMoney(weeklyQuota, 2)), walk.RGB(255, 173, 47)},
		{"钱包余额", panelMoney(s.Wallet, 2), panelWalletSubText(s.Wallet), walk.RGB(40, 184, 255)},
		{"周额度", panelMoney(weeklyQuota, 0), fmt.Sprintf("剩余 %s", panelMoney(weeklyRemaining, 2)), walk.RGB(49, 223, 154)},
		{"月总额度", monthlyValue, monthlySub, walk.RGB(155, 124, 255)},
	}
}

func panelMoney(v float64, digits int) string {
	return fmt.Sprintf("$%s", panelCommaFloat(v, digits))
}

func panelCommaFloat(v float64, digits int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	s := fmt.Sprintf("%.*f", digits, v)
	dot := len(s)
	for i, ch := range s {
		if ch == '.' {
			dot = i
			break
		}
	}
	intPart, frac := s[:dot], s[dot:]
	out := ""
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			out += ","
		}
		out += string(ch)
	}
	return sign + out + frac
}

func panelWalletSubText(wallet float64) string {
	if wallet == 0 {
		return "额度用完自动消耗"
	}
	return "信用 + 福利"
}

func panelNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
