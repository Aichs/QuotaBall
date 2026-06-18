package sub2

import (
	"encoding/json"
	"testing"
	"time"

	"quotaball/internal/config"
)

func TestToSnapshotMapsBalanceAndDailyWeeklyMonthlyQuotas(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	snap := ToSnapshot(
		User{ID: 7, Email: "user@example.test", Balance: 8.5},
		SubscriptionSummary{
			ActiveCount:  1,
			TotalUsedUSD: 1500,
			Subscriptions: []SubscriptionSummaryItem{{
				ID:              11,
				GroupName:       "Pro",
				DailyUsedUSD:    25,
				DailyLimitUSD:   100,
				WeeklyUsedUSD:   140,
				WeeklyLimitUSD:  700,
				MonthlyUsedUSD:  1500,
				MonthlyLimitUSD: 3000,
				ExpiresAt:       "2026-07-18T12:00:00Z",
			}},
		},
		[]SubscriptionProgressInfo{{
			Subscription: &UserSubscription{
				ID:        11,
				StartsAt:  "2026-06-18T12:00:00Z",
				ExpiresAt: "2026-07-18T12:00:00Z",
				Group: &Group{
					Name:     "Pro",
					Platform: "openai",
				},
			},
			Progress: &SubscriptionProgress{
				SubscriptionID: 11,
				Daily:          &QuotaWindow{Used: 25, Limit: nullableFloat{Valid: true, Value: 100}, Percentage: 25},
				Weekly:         &QuotaWindow{Used: 140, Limit: nullableFloat{Valid: true, Value: 700}, Percentage: 20},
				Monthly:        &QuotaWindow{Used: 1500, Limit: nullableFloat{Valid: true, Value: 3000}, Percentage: 50},
				ExpiresAt:      "2026-07-18T12:00:00Z",
				DaysRemaining:  nullableInt{Valid: true, Value: 30},
			},
		}},
		now,
	)

	if !snap.LoggedIn || !snap.OK || snap.Provider != config.ProviderSub2 {
		t.Fatalf("snapshot auth state = loggedIn=%v ok=%v provider=%q", snap.LoggedIn, snap.OK, snap.Provider)
	}
	if snap.Wallet != 8.5 || snap.Spend != 1500 {
		t.Fatalf("snapshot money = wallet=%v spend=%v, want balance and monthly used", snap.Wallet, snap.Spend)
	}
	if snap.Summary.TotalDailyQuotaUSD != 100 || snap.Summary.TotalDailyUsedUSD != 25 || snap.Summary.TotalDailyRemainingUSD != 75 {
		t.Fatalf("daily summary = %#v", snap.Summary)
	}
	if snap.Summary.TotalWeeklyQuotaUSD != 700 || snap.Summary.TotalWeeklyUsedUSD != 140 || snap.Summary.TotalWeeklyRemainingUSD != 560 {
		t.Fatalf("weekly summary = %#v", snap.Summary)
	}
	if snap.Summary.TotalMonthlyQuotaUSD != 3000 || snap.Summary.TotalMonthlyUsedUSD != 1500 || snap.Summary.TotalMonthlyRemainingUSD != 1500 {
		t.Fatalf("monthly summary = %#v", snap.Summary)
	}
	if len(snap.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(snap.Subscriptions))
	}
	sub := snap.Subscriptions[0]
	if sub.Name != "Pro" || sub.DailyPercent != 25 || sub.WeeklyPercent != 20 || sub.MonthlyPercent != 50 {
		t.Fatalf("subscription = %#v", sub)
	}
	if sub.Start != "2026-06-18" || sub.End != "2026-07-18" || sub.DaysLeft != 30 {
		t.Fatalf("subscription dates = start=%q end=%q days=%v", sub.Start, sub.End, sub.DaysLeft)
	}
	if len(sub.Routes) != 1 || sub.Routes[0] != "openai" {
		t.Fatalf("subscription routes = %#v", sub.Routes)
	}
}

func TestToSnapshotUsesProgressWindowUSDFields(t *testing.T) {
	var progress []SubscriptionProgressInfo
	if err := json.Unmarshal([]byte(`[
		{
			"subscription": {
				"id": 11,
				"starts_at": "2026-06-18T12:00:00Z",
				"expires_at": "2026-07-18T12:00:00Z",
				"group": {"name": "Pro", "platform": "openai"}
			},
			"progress": {
				"subscription_id": 11,
				"daily": {"used_usd": "25", "limit_usd": "100", "remaining_usd": "75", "percentage": 25},
				"weekly": {"used_usd": "140", "limit_usd": "700", "remaining_usd": "560", "percentage": 20},
				"monthly": {"used_usd": "1500", "limit_usd": "3000", "remaining_usd": "1500", "percentage": 50},
				"expires_at": "2026-07-18T12:00:00Z",
				"days_remaining": 30
			}
		}
	]`), &progress); err != nil {
		t.Fatal(err)
	}

	snap := ToSnapshot(
		User{ID: 7, Email: "user@example.test", Balance: 8.5},
		SubscriptionSummary{
			TotalUsedUSD: 1500,
			Subscriptions: []SubscriptionSummaryItem{{
				ID:              11,
				GroupName:       "Pro",
				DailyLimitUSD:   100,
				WeeklyLimitUSD:  700,
				MonthlyLimitUSD: 3000,
			}},
		},
		progress,
		time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC),
	)

	if len(snap.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(snap.Subscriptions))
	}
	sub := snap.Subscriptions[0]
	if sub.DailyUsed != 25 || sub.DailyRemaining != 75 || sub.DailyPercent != 25 {
		t.Fatalf("daily quota = %#v", sub)
	}
	if sub.WeeklyUsed != 140 || sub.WeeklyRemaining != 560 || sub.WeeklyPercent != 20 {
		t.Fatalf("weekly quota = %#v", sub)
	}
	if sub.MonthlyUsed != 1500 || sub.MonthlyRemaining != 1500 || sub.MonthlyPercent != 50 {
		t.Fatalf("monthly quota = %#v", sub)
	}
	if snap.Summary.TotalDailyUsedUSD != 25 || snap.Summary.TotalDailyRemainingUSD != 75 {
		t.Fatalf("daily summary should aggregate progress values: %#v", snap.Summary)
	}
	if snap.Summary.TotalWeeklyUsedUSD != 140 || snap.Summary.TotalWeeklyRemainingUSD != 560 {
		t.Fatalf("weekly summary should aggregate progress values: %#v", snap.Summary)
	}
	if snap.Summary.TotalMonthlyUsedUSD != 1500 || snap.Summary.TotalMonthlyRemainingUSD != 1500 {
		t.Fatalf("monthly summary should aggregate progress values: %#v", snap.Summary)
	}
}
