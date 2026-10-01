package billing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSubscriptionPurchaseIsAtomicIdempotentAndModelScoped(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, userID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Point Plan Admin','admin','active'),
		($4,$5,$6,'Point Plan User','creator','active')`,
		adminID, adminID.String()+"@test.local", "point_admin_"+adminID.String()[:8],
		userID, userID.String()+"@test.local", "point_user_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}

	providerID, chatModelID, imageModelID, videoModelID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_configs(id,name,protocol,endpoint,runtime_provider,admin_enabled,created_by,updated_by)
		VALUES($1,'Point Meter Provider','openai_responses','https://provider.test/v1','openai',true,$2,$2)`, providerID, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_config_models(id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,admin_enabled,created_by,updated_by) VALUES
		($3,$1,'chat','point-chat','Point Chat','Point-scoped chat model.',0,true,$2,$2),
		($4,$1,'image','point-image','Point Image','Point-scoped image model.',0,true,$2,$2),
		($5,$1,'video','point-video','Point Video','Excluded video model.',0,true,$2,$2)`,
		providerID, adminID, chatModelID, imageModelID, videoModelID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pricing := []struct {
		id   uuid.UUID
		mode string
		rule billing.ModelPointPricing
	}{
		{chatModelID, "chat", billing.ModelPointPricing{InputPointsPer1KTokens: 10, OutputPointsPer1KTokens: 30, MinimumPoints: 5}},
		{imageModelID, "image", billing.ModelPointPricing{MinimumPoints: 20, ImageResolutionPrices: []billing.ImageResolutionPrice{{Resolution: "1024x1024", Points: 50}, {Resolution: "1536x1024", Points: 80}}}},
		{videoModelID, "video", billing.ModelPointPricing{PointsPerSecond: 4, MinimumPoints: 20}},
	}
	for _, item := range pricing {
		if _, err := billing.UpsertModelPointPricing(ctx, tx, adminID, item.id, item.mode, item.rule); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	service := billing.NewService(pool)
	plan, err := service.CreateSubscriptionPlan(ctx, adminID, billing.SubscriptionPlanInput{
		TierCode: "point_test_" + uuid.NewString()[:8], Name: "Point Test", Description: "A deterministic subscription purchase fixture.",
		PriceCents: 1500, Currency: "USD", IncludedPoints: 20000, BillingPeriodDays: 30, SortOrder: 99, Active: true,
		ModelIDs: []uuid.UUID{chatModelID, imageModelID},
	}, "points-plan-create")
	if err != nil {
		t.Fatal(err)
	}
	description, includedPoints := "Updated plan terms used for the purchase.", int64(24000)
	updated, err := service.UpdateSubscriptionPlan(ctx, adminID, plan.ID, billing.SubscriptionPlanUpdate{ExpectedVersion: plan.Version, Description: &description, IncludedPoints: &includedPoints}, "points-plan-update")
	if err != nil || updated.Description != description || updated.IncludedPoints != includedPoints || len(updated.ModelIDs) != 2 {
		t.Fatalf("update subscription plan: %#v err=%v", updated, err)
	}

	overview, err := service.PurchaseSubscription(ctx, userID, plan.ID, "purchase-plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if overview.CurrentSubscription == nil || overview.CurrentSubscription.PlanID != plan.ID || overview.Account.BalancePoints != 34000 || overview.Account.ReservedPoints != 0 {
		t.Fatalf("unexpected purchased subscription overview: %#v", overview)
	}
	assertWalletAndPointBalances(t, ctx, pool, userID, 248500, 34000)

	replayed, err := service.PurchaseSubscription(ctx, userID, plan.ID, "purchase-plan-001")
	if err != nil || replayed.Account.BalancePoints != 34000 {
		t.Fatalf("idempotent subscription replay changed points: %#v err=%v", replayed.Account, err)
	}
	var operationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT purchase_operation_id FROM user_subscriptions WHERE user_id=$1 AND idempotency_key='purchase-plan-001'`, userID).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	var pointCredits, walletDebits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='subscription_credit'`, userID, operationID).Scan(&pointCredits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='subscription_purchase'`, userID, operationID).Scan(&walletDebits); err != nil {
		t.Fatal(err)
	}
	if pointCredits != 1 || walletDebits != 1 {
		t.Fatalf("subscription purchase was not recorded exactly once: points=%d wallet=%d", pointCredits, walletDebits)
	}
	if _, err := service.PurchaseSubscription(ctx, userID, plan.ID, "purchase-plan-002"); !errors.Is(err, billing.ErrSubscriptionActive) {
		t.Fatalf("active plan could be purchased twice: %v", err)
	}
	var otherPlanID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM subscription_plans WHERE tier_code='studio'`).Scan(&otherPlanID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PurchaseSubscription(ctx, userID, otherPlanID, "purchase-plan-001"); !errors.Is(err, billing.ErrSubscriptionReplay) {
		t.Fatalf("idempotency key accepted a different plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_accounts SET balance_cents=100,reserved_cents=0 WHERE user_id=$1 AND currency='USD'`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PurchaseSubscription(ctx, userID, otherPlanID, "purchase-plan-003"); !errors.Is(err, billing.ErrInsufficientFunds) {
		t.Fatalf("subscription purchase ignored wallet funds: %v", err)
	}
	assertWalletAndPointBalances(t, ctx, pool, userID, 100, 34000)
	current, err := service.PointOverview(ctx, userID)
	if err != nil || current.CurrentSubscription == nil || current.CurrentSubscription.PlanID != plan.ID {
		t.Fatalf("failed purchase changed the active subscription: %#v err=%v", current.CurrentSubscription, err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := billing.ReserveGenerationPointsTx(ctx, tx, userID, uuid.New(), &videoModelID, "", "video", billing.MeterInput{DurationSeconds: 10}); !errors.Is(err, billing.ErrModelNotIncluded) {
		t.Fatalf("subscription model allowlist did not reject an excluded model: %v", err)
	}
	if estimate, _, err := billing.ReserveGenerationPointsTx(ctx, tx, userID, uuid.New(), &imageModelID, "", "image", billing.MeterInput{AspectRatio: "16:9", ImageCount: 2}); err != nil || estimate != 160 {
		t.Fatalf("included image model was not metered from its configured rule: estimate=%d err=%v", estimate, err)
	}
}

func TestModelPointMeteringUsesTokensResolutionCountAndDuration(t *testing.T) {
	chat := billing.ModelPointPricing{Mode: "chat", InputPointsPer1KTokens: 10, OutputPointsPer1KTokens: 30, MinimumPoints: 5}
	if estimate, _ := billing.EstimatePoints(chat, billing.MeterInput{PromptCharacters: 4000, ResponseLength: "short"}); estimate != 26 {
		t.Fatalf("unexpected chat estimate: %d", estimate)
	}
	if actual := billing.ActualPoints(chat, billing.MeterInput{}, billing.UsageMetrics{InputTokens: 1500, OutputTokens: 500, ProviderReported: true}); actual != 30 {
		t.Fatalf("unexpected actual token charge: %d", actual)
	}

	image := billing.ModelPointPricing{Mode: "image", MinimumPoints: 20, ImageResolutionPrices: []billing.ImageResolutionPrice{{Resolution: "1024x1024", Points: 50}, {Resolution: "1536x1024", Points: 80}}}
	if estimate, resolution := billing.EstimatePoints(image, billing.MeterInput{AspectRatio: "16:9", ImageCount: 2}); estimate != 160 || resolution != "1536x1024" {
		t.Fatalf("unexpected image estimate: points=%d resolution=%q", estimate, resolution)
	}
	if actual := billing.ActualPoints(image, billing.MeterInput{ImageCount: 2}, billing.UsageMetrics{Width: 640, Height: 360, ImageCount: 2, ProviderReported: true}); actual != 100 {
		t.Fatalf("unexpected nearest-resolution image charge: %d", actual)
	}

	video := billing.ModelPointPricing{Mode: "video", PointsPerSecond: 4, MinimumPoints: 20}
	if actual := billing.ActualPoints(video, billing.MeterInput{}, billing.UsageMetrics{DurationSeconds: 3, ProviderReported: true}); actual != 20 {
		t.Fatalf("video minimum charge was not applied: %d", actual)
	}
	if actual := billing.ActualPoints(video, billing.MeterInput{}, billing.UsageMetrics{DurationSeconds: 12, ProviderReported: true}); actual != 48 {
		t.Fatalf("unexpected video duration charge: %d", actual)
	}

	music := billing.ModelPointPricing{Mode: "music", PointsPerSecond: 2, MinimumPoints: 10}
	if actual := billing.ActualPoints(music, billing.MeterInput{}, billing.UsageMetrics{DurationSeconds: 12, ProviderReported: true}); actual != 24 {
		t.Fatalf("unexpected music duration charge: %d", actual)
	}
}

func TestCaptureGenerationPointsCannotSpendAnotherReservation(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	userID, generationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Reservation User','member','active')`, userID, userID.String()+"@test.local", "reservation_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE point_accounts SET balance_points=100,reserved_points=80 WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt) VALUES($1,$2,'video','local_test','reservation-model','reservation test')`, generationID, userID); err != nil {
		t.Fatal(err)
	}
	var userSubscriptionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM user_subscriptions WHERE user_id=$1 AND status='active' ORDER BY created_at DESC LIMIT 1`, userID).Scan(&userSubscriptionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO point_reservations(user_id,generation_id,subscription_id,held_points,pricing_snapshot) VALUES($1,$2,$3,20,$4)`, userID, generationID, userSubscriptionID, []byte(`{"Rule":{"Mode":"video","PointsPerSecond":10,"MinimumPoints":1},"EstimateInput":{}}`)); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = billing.CaptureGenerationPointsTx(ctx, tx, generationID, billing.UsageMetrics{DurationSeconds: 5, ProviderReported: true})
	if !errors.Is(err, billing.ErrInsufficientFunds) {
		_ = tx.Rollback(ctx)
		t.Fatalf("capture spent points reserved for another generation: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var balance, reserved int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT p.balance_points,p.reserved_points,r.status FROM point_accounts p JOIN point_reservations r ON r.user_id=p.user_id WHERE p.user_id=$1 AND r.generation_id=$2`, userID, generationID).Scan(&balance, &reserved, &status); err != nil {
		t.Fatal(err)
	}
	if balance != 100 || reserved != 80 || status != "held" {
		t.Fatalf("failed capture changed balances: balance=%d reserved=%d status=%s", balance, reserved, status)
	}
}

func assertWalletAndPointBalances(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, walletCents, points int64) {
	t.Helper()
	var actualWallet, actualPoints int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, userID).Scan(&actualWallet); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_points FROM point_accounts WHERE user_id=$1`, userID).Scan(&actualPoints); err != nil {
		t.Fatal(err)
	}
	if actualWallet != walletCents || actualPoints != points {
		t.Fatalf("unexpected balances: wallet=%d points=%d", actualWallet, actualPoints)
	}
}
