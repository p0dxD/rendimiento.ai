package store

import (
	"context"
	"testing"
	"time"
)

// AI settings default to the local model; spending adds up by who paid,
// this month only, and the platform's bill sums every organization.
func TestAI(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if a, err := s.AISettingsOf(ctx, DefaultOrg); err != nil || a.Provider != AILocal || a.CapMicros != 1_000_000 {
		t.Fatalf("defaults: %+v %v", a, err)
	}
	if err := s.SetAIAllowance(ctx, DefaultOrg, 500_000, "operador"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAISettings(ctx, AISettings{OrgID: DefaultOrg, Provider: AIPlatform, CapMicros: 2_000_000}, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	a, err := s.AISettingsOf(ctx, DefaultOrg)
	if err != nil || a.Provider != AIPlatform || a.AllowanceMicros != 500_000 || a.CapMicros != 2_000_000 {
		t.Fatalf("saved: %+v %v (saving keeps the allowance)", a, err)
	}
	other, err := s.CreateOrg(ctx, "otra", "Otra", "ana")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []AIUsage{
		{OrgID: DefaultOrg, Source: "x", Provider: AIPlatform, Model: "m", CostMicros: 300, OK: true},
		{OrgID: DefaultOrg, Source: "x", Provider: AIOwn, Model: "m", CostMicros: 70, OK: true},
		{OrgID: DefaultOrg, Source: "x", Provider: AILocal, Model: "m", OK: true},
		{OrgID: other.ID, Source: "x", Provider: AIPlatform, Model: "m", CostMicros: 1000, OK: true},
	} {
		if err := s.RecordAIUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	own, platform, err := s.AISpent(ctx, DefaultOrg, time.Now())
	if err != nil || own != 70 || platform != 300 {
		t.Fatalf("spent: own %d platform %d (%v)", own, platform, err)
	}
	if n, _ := s.AIPlatformSpent(ctx, time.Now()); n != 1300 {
		t.Fatalf("platform bill: %d", n)
	}
	if own, platform, _ := s.AISpent(ctx, DefaultOrg, time.Now().AddDate(0, 1, 0)); own != 0 || platform != 0 {
		t.Fatalf("next month starts at zero: %d %d", own, platform)
	}
	if list, err := s.AIUsageOf(ctx, other.ID, 10); err != nil || len(list) != 1 || list[0].CostMicros != 1000 {
		t.Fatalf("usage of the other: %+v %v", list, err)
	}
}
