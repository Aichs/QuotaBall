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
