package database_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPhysicalFixturePurgePreservesPersonalDataAndGuards(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	body, err := os.ReadFile("../../../scripts/sql/purge-retired-demo.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacy := "00000000-0000-4000-8000-000000000001"
	personal, asset, demand, keepEvent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,handle,display_name,status) VALUES($1,'deleted+'||$1::uuid::text||'@hcai.invalid','retired_fixture','Deleted account','deleted')`, legacy)
	exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,'personal@example.test','personal','Personal account')`, personal)
	exec(`INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,'personal-session',now()+interval '1 day')`, personal)
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type) VALUES($1,$2,'image','Old asset','/media/home-cinematic.jpg','image/jpeg','demo')`, asset, legacy)
	exec(`INSERT INTO works(author_id,asset_id,title,model_name,ai_disclosure,status) VALUES($1,$2,'Old work','fixture','fixture','removed')`, legacy, asset)
	exec(`INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,deadline,status) VALUES($1,$2,'Old task','Old brief','image',100,now()+interval '1 day','cancelled')`, demand, legacy)
	exec(`INSERT INTO task_events(demand_id,actor_id,kind,to_status) VALUES($1,$2,'published','open')`, demand, legacy)
	exec(`UPDATE system_settings SET updated_by=$1`, legacy)
	exec(`INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id) VALUES($1,'old.action','user',$1,'retired-event')`, legacy)
	exec(`INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,request_id) VALUES($1,$2,'personal.action','user',$2,'personal-event')`, keepEvent, personal)
	var personalBefore, settingsBefore string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(u)::text FROM users u WHERE id=$1`, personal).Scan(&personalBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(s)-'updated_by')::text FROM system_settings s`).Scan(&settingsBefore); err != nil {
		t.Fatal(err)
	}

	// An unexpected polymorphic reference must roll back deletion and guard changes.
	exec(`INSERT INTO jobs(kind,payload) VALUES('fixture.guard',jsonb_build_object('ownerId',$1::text))`, legacy)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL hcai.retired_fixture_purge='test'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(body)); err == nil {
		t.Fatal("expected refusal for unhandled reference")
	}
	_ = tx.Rollback(ctx)
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, legacy).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("rollback failed: %d %v", remaining, err)
	}
	exec(`DELETE FROM jobs WHERE kind='fixture.guard'`)

	for i := 0; i < 2; i++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SET LOCAL hcai.retired_fixture_purge='test'`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var personalAfter, settingsAfter string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(u)::text FROM users u WHERE id=$1`, personal).Scan(&personalAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (to_jsonb(s)-'updated_by')::text FROM system_settings s`).Scan(&settingsAfter); err != nil {
		t.Fatal(err)
	}
	if personalBefore != personalAfter || settingsBefore != settingsAfter {
		t.Fatal("unrelated user or settings changed")
	}
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM users`, 1}, {`SELECT count(*) FROM assets`, 0},
		{`SELECT count(*) FROM works`, 0}, {`SELECT count(*) FROM demands`, 0},
		{`SELECT count(*) FROM task_events`, 0}, {`SELECT count(*) FROM sessions`, 1},
		{`SELECT count(*) FROM audit_events WHERE request_id='personal-event'`, 1},
		{`SELECT count(*) FROM pg_trigger WHERE tgname IN ('task_events_immutable','audit_events_immutable') AND tgrelid IN ('task_events'::regclass,'audit_events'::regclass) AND tgenabled='O'`, 2},
		{`WITH checked AS (SELECT *,lag(event_hash) OVER(ORDER BY sequence) AS expected_previous FROM audit_events) SELECT count(*) FROM checked WHERE COALESCE(previous_hash,'')<>COALESCE(expected_previous,'') OR event_hash<>audit_event_hash(sequence,previous_hash,id,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at)`, 0},
	} {
		var got int
		if err := pool.QueryRow(ctx, check.query).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s: got %d want %d (%v)", check.query, got, check.want, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE id=$1`, keepEvent); err == nil {
		t.Fatal("audit guard disabled after purge")
	}
}
