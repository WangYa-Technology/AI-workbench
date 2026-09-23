package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
)

func TestRetireSharedIdentitiesPreservesPersonalAccountsAndEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	// Recreate a pre-retirement database in this isolated schema only.
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version='0070_retire_shared_identities.up.sql'`); err != nil {
		t.Fatal(err)
	}
	legacyIDs := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		uuid.MustParse("00000000-0000-4000-8000-000000000002"),
	}
	personalID := uuid.New()
	for i, id := range append(legacyIDs, personalID) {
		email := []string{"studio@demo.hcai.local", "creator@DEMO.HCAI.LOCAL", "personal@example.com"}[i]
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,password_hash,email_verified_at)
		  VALUES($1,$2,$3,'Original name','admin','original-hash',now())`, id, email, "user_"+id.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '1 day')`, id, id.String()); err != nil {
			t.Fatal(err)
		}
	}
	assetID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type)
	  VALUES($1,$2,'image','Retired source','/media/test.jpg','image/jpeg','demo')`, assetID, legacyIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO works(author_id,asset_id,title,prompt,model_name,status,ai_disclosure)
	  VALUES($1,$2,'Retired work','Old prompt','Old source','published','Test')`, legacyIDs[0], assetID); err != nil {
		t.Fatal(err)
	}
	actionID, challengeID, notificationID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO identity_email_actions(id,user_id,kind,email_snapshot,locale,expires_at,
	  token_hash,token_nonce,token_ciphertext) VALUES($1,$2,'password_reset','studio@demo.hcai.local','en-US',now()+interval '1 hour',
	  repeat('a',64),decode(repeat('00',12),'hex'),decode(repeat('00',32),'hex'))`, actionID, legacyIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity_email_delivery_attempts(action_id,attempt_number,adapter,status,error_code)
	  VALUES($1,1,'smtp','failed','temporary_failure')`, actionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity_auth_challenges(id,purpose,email_snapshot,locale,expires_at,
	  code_hash,code_nonce,code_ciphertext) VALUES($1,'login_code','studio@demo.hcai.local','en-US',now()+interval '1 hour',
	  repeat('b',64),decode(repeat('00',12),'hex'),decode(repeat('00',32),'hex'))`, challengeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notifications(id,user_id,kind,title,body,target_path)
	  VALUES($1,$2,'generation.completed','Old notification','Body','/workspace/assets')`, notificationID, legacyIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,payload) VALUES
	  ('identity.email_action.deliver',jsonb_build_object('actionId',$1::text)),
	  ('identity.auth_challenge.deliver',jsonb_build_object('challengeId',$2::text)),
	  ('notification.deliver',jsonb_build_object('notificationId',$3::text))`, actionID, challengeID, notificationID); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name, query string
		want        int
	}{
		{"anonymized identities", `SELECT count(*) FROM users WHERE status='deleted' AND role='member' AND display_name='Deleted account' AND password_hash IS NULL AND email_verified_at IS NULL AND email LIKE 'deleted+%@hcai.invalid'`, 2},
		{"old addresses", `SELECT count(*) FROM users WHERE lower(email) LIKE '%@demo.hcai.local'`, 0},
		{"personal account", `SELECT count(*) FROM users WHERE email='personal@example.com' AND status='active' AND role='admin' AND password_hash='original-hash' AND display_name='Original name' AND email_verified_at IS NOT NULL`, 1},
		{"personal session", `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.email='personal@example.com' AND s.revoked_at IS NULL`, 1},
		{"retired sessions", `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.status='deleted'`, 0},
		{"blocked asset", `SELECT count(*) FROM assets WHERE scan_status='rejected'`, 1},
		{"retired work", `SELECT count(*) FROM works WHERE status='removed' AND prompt IS NULL AND prompt_visibility='private'`, 1},
		{"reset token", `SELECT count(*) FROM identity_email_actions WHERE status='cancelled' AND token_hash IS NULL AND token_nonce IS NULL AND token_ciphertext IS NULL AND email_snapshot LIKE 'deleted+%@hcai.invalid'`, 1},
		{"login code", `SELECT count(*) FROM identity_auth_challenges WHERE status='cancelled' AND code_hash IS NULL AND code_nonce IS NULL AND code_ciphertext IS NULL AND email_snapshot LIKE 'deleted+%@hcai.invalid'`, 1},
		{"immutable delivery evidence", `SELECT count(*) FROM identity_email_delivery_attempts WHERE adapter='smtp' AND status='failed' AND error_code='temporary_failure'`, 1},
		{"notifications", `SELECT count(*) FROM notifications`, 0},
		{"cancelled deliveries", `SELECT count(*) FROM jobs WHERE status='cancelled'`, 3},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			var got int
			if err := pool.QueryRow(ctx, check.query).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != check.want {
				t.Fatalf("got %d, want %d", got, check.want)
			}
		})
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
