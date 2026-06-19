package krill

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseSubscriptionBuildsMonitorState(t *testing.T) {
	const raw = `{
		"summary":{"total_used_usd":12.5,"total_daily_quota_usd":100,"total_forwarded_remaining_usd":7},
		"credit_balance_usd":3,
		"welfare_balance_usd":2.5,
		"subscriptions":[{
			"subscription_id":"sub_1",
			"subscription_start_at":"2026-06-01T00:00:00Z",
			"subscription_end_at":"2026-06-10T00:00:00Z",
			"plan":{"name":"Pro","entry_route_keys":["claude","gpt"]},
			"quota":{
				"daily_limit_usd":50,
				"used_usd":20,
				"remaining_usd":30,
				"forwarded_limit_usd":10,
				"forwarded_used_usd":3,
				"forwarded_remaining_usd":7
			}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	if snap.Spend != 12.5 || snap.Wallet != 5.5 {
		t.Fatalf("money fields not parsed: %#v", snap)
	}
	if snap.RemainingDaily() != 87.5 {
		t.Fatalf("RemainingDaily = %v", snap.RemainingDaily())
	}
	if len(snap.Subscriptions) != 1 {
		t.Fatalf("subscription count = %d", len(snap.Subscriptions))
	}
	sub := snap.Subscriptions[0]
	if sub.Name != "Pro" || sub.DailyPercent != 40 || sub.ForwardedPercent != 30 {
		t.Fatalf("subscription fields not normalized: %#v", sub)
	}
	if sub.DaysLeft != 2 {
		t.Fatalf("DaysLeft = %v, want 2", sub.DaysLeft)
	}
}

func TestParseSubscriptionAcceptsStringAndNullMoneyFields(t *testing.T) {
	const raw = `{
		"summary":{"total_used_usd":"12.5","total_daily_quota_usd":"100","total_forwarded_remaining_usd":null},
		"credit_balance_usd":"3.25",
		"welfare_balance_usd":null,
		"subscriptions":[{
			"subscription_id":"sub_1",
			"subscription_start_at":"2026-06-01T00:00:00Z",
			"subscription_end_at":"2026-06-10T00:00:00Z",
			"plan":{"name":"Pro","entry_route_keys":["claude"]},
			"quota":{
				"daily_limit_usd":"50",
				"used_usd":"20",
				"remaining_usd":30,
				"forwarded_limit_usd":null,
				"forwarded_used_usd":"0",
				"forwarded_remaining_usd":null
			}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	if snap.Spend != 12.5 {
		t.Fatalf("Spend = %v, want 12.5", snap.Spend)
	}
	if snap.Wallet != 3.25 {
		t.Fatalf("Wallet = %v, want 3.25", snap.Wallet)
	}
	if got := snap.Subscriptions[0].DailyPercent; got != 40 {
		t.Fatalf("DailyPercent = %v, want 40", got)
	}
}

func TestParseSubscriptionAcceptsNumericSubscriptionID(t *testing.T) {
	const raw = `{
		"summary":{"total_used_usd":0,"total_daily_quota_usd":1},
		"subscriptions":[{
			"subscription_id":12345,
			"subscription_start_at":"2026-06-01T00:00:00Z",
			"subscription_end_at":"2026-06-10T00:00:00Z",
			"plan":{"name":"Pro"},
			"quota":{"daily_limit_usd":1}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	if got := snap.Subscriptions[0].ID; got != "12345" {
		t.Fatalf("ID = %q, want 12345", got)
	}
}

func TestParseSubscriptionBuildsWeeklyAndMonthlyQuotaState(t *testing.T) {
	const raw = `{
		"summary":{
			"total_used_usd":"384.649625",
			"total_daily_quota_usd":"1200.000000",
			"total_remaining_usd":"815.350375"
		},
		"credit_balance_usd":"0.000000",
		"welfare_balance_usd":"0",
		"subscriptions":[{
			"subscription_id":5344,
			"subscription_start_at":"2026-06-10T04:23:51.603582Z",
			"subscription_end_at":"2026-07-10T04:23:51.603582Z",
			"total_used_usd":"384.649625",
			"plan":{
				"name":"轻享月卡",
				"billing_type":"usd_weekly",
				"duration_days":30,
				"entry_route_keys":["直连","cdn"],
				"total_credits":null
			},
			"quota":{
				"daily_limit_usd":"600.000000",
				"remaining_usd":"215.350375",
				"used_usd":"384.649625",
				"window_start_at":"2026-06-09T16:00:00Z",
				"window_reset_at":"2026-06-16T16:00:00Z"
			}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 13, 8, 0, 0, 0, time.UTC))

	if snap.Summary.TotalWeeklyQuotaUSD != 1200 {
		t.Fatalf("TotalWeeklyQuotaUSD = %v, want 1200", snap.Summary.TotalWeeklyQuotaUSD)
	}
	if snap.Summary.TotalWeeklyRemainingUSD != 815.350375 {
		t.Fatalf("TotalWeeklyRemainingUSD = %v, want 815.350375", snap.Summary.TotalWeeklyRemainingUSD)
	}
	sub := snap.Subscriptions[0]
	if sub.WeeklyLimit != 600 || sub.WeeklyUsed != 384.649625 || sub.WeeklyRemaining != 215.350375 {
		t.Fatalf("weekly fields = limit %v used %v remaining %v", sub.WeeklyLimit, sub.WeeklyUsed, sub.WeeklyRemaining)
	}
	if sub.MonthlyLimit != 2400 || sub.MonthlyUsed != 384.649625 || sub.MonthlyPercent != 16 {
		t.Fatalf("monthly fields = limit %v used %v pct %v", sub.MonthlyLimit, sub.MonthlyUsed, sub.MonthlyPercent)
	}
}

func TestParseSubscriptionPrioritizesCurrentQuotaWindow(t *testing.T) {
	const raw = `{
		"summary":{
			"total_used_usd":"20",
			"total_daily_quota_usd":"1200",
			"total_remaining_usd":"1180"
		},
		"subscriptions":[{
			"subscription_id":5344,
			"subscription_start_at":"2026-06-10T04:23:51Z",
			"subscription_end_at":"2026-07-10T04:23:51Z",
			"total_used_usd":"400",
			"plan":{"name":"旧窗口月卡","billing_type":"usd_weekly","duration_days":30},
			"quota":{
				"daily_limit_usd":"600",
				"used_usd":"400",
				"remaining_usd":"200",
				"window_start_at":"2026-06-09T16:00:00Z",
				"window_reset_at":"2026-06-16T16:00:00Z"
			}
		},{
			"subscription_id":6911,
			"subscription_start_at":"2026-06-13T06:45:41Z",
			"subscription_end_at":"2026-07-13T06:45:41Z",
			"total_used_usd":"20",
			"plan":{"name":"当前窗口月卡","billing_type":"usd_weekly","duration_days":30},
			"quota":{
				"daily_limit_usd":"600",
				"used_usd":"20",
				"remaining_usd":"580",
				"window_start_at":"2026-06-16T16:00:00Z",
				"window_reset_at":"2026-06-23T16:00:00Z"
			}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC))

	if len(snap.Subscriptions) != 2 {
		t.Fatalf("subscription count = %d, want 2", len(snap.Subscriptions))
	}
	if got := snap.Subscriptions[0].ID; got != "6911" {
		t.Fatalf("first subscription = %q, want current window subscription 6911", got)
	}
	if !snap.Subscriptions[0].CurrentWindow {
		t.Fatalf("current subscription was not marked CurrentWindow: %#v", snap.Subscriptions[0])
	}
	if snap.Subscriptions[1].CurrentWindow {
		t.Fatalf("stale subscription was marked CurrentWindow: %#v", snap.Subscriptions[1])
	}
}

func TestParseSubscriptionPrioritizesCurrentWindowUsageOverHistoricalTotal(t *testing.T) {
	const raw = `{
		"summary":{
			"total_used_usd":"345.624580",
			"total_daily_quota_usd":"1692.000000",
			"total_remaining_usd":"1346.375420"
		},
		"subscriptions":[{
			"subscription_id":5344,
			"subscription_start_at":"2026-06-10T04:23:51Z",
			"subscription_end_at":"2026-07-10T04:23:51Z",
			"total_used_usd":"792.000000",
			"plan":{"name":"旧月卡","billing_type":"usd_weekly","duration_days":30},
			"quota":{
				"daily_limit_usd":"792.000000",
				"used_usd":"0.000000",
				"remaining_usd":"792.000000",
				"window_start_at":"2026-06-16T16:00:00Z",
				"window_reset_at":"2026-06-23T16:00:00Z"
			}
		},{
			"subscription_id":6911,
			"subscription_start_at":"2026-06-13T06:45:41Z",
			"subscription_end_at":"2026-07-13T06:45:41Z",
			"total_used_usd":"345.624580",
			"plan":{"name":"正在消耗月卡","billing_type":"usd_weekly","duration_days":30},
			"quota":{
				"daily_limit_usd":"900.000000",
				"used_usd":"345.624580",
				"remaining_usd":"554.375420",
				"window_start_at":"2026-06-12T16:00:00Z",
				"window_reset_at":"2026-06-19T16:00:00Z"
			}
		}]
	}`
	var payload SubscriptionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	snap := payload.ToSnapshot(time.Date(2026, 6, 19, 11, 23, 0, 0, time.UTC))

	if got := snap.Subscriptions[0].ID; got != "6911" {
		t.Fatalf("first subscription = %q, want currently consuming subscription 6911", got)
	}
	if got := snap.Subscriptions[0].WeeklyUsed; got != 345.62458 {
		t.Fatalf("current window used = %v, want 345.62458", got)
	}
}
