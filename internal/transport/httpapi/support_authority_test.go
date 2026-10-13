package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSupportMutationRechecksAuthorityAfterAdmission(t *testing.T) {
	for _, action := range []string{"reply", "resolve"} {
		for _, revoke := range []string{"role", "status", "permission"} {
			t.Run(action+"/"+revoke, func(t *testing.T) {
				pool, cleanup := httpTestPool(t)
				t.Cleanup(cleanup)
				traced, entered, release := testutil.GateQuery(t, pool, "SELECT requester_id,status,version FROM support_cases WHERE id=$1 FOR UPDATE")
				server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, traced, slog.New(slog.NewTextHandler(io.Discard, nil))))
				t.Cleanup(server.Close)
				client := testHTTPClient(t)
				actor := registerGovernanceUser(t, client, server.URL, "support_authority")
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				exec := func(sql string, args ...any) {
					t.Helper()
					if _, err := pool.Exec(ctx, sql, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
				caseID := uuid.New()
				exec(`INSERT INTO support_cases(id,requester_id,category,subject,details,locale) VALUES($1,$2,'task_or_order','Order support','Please investigate this resource order.','en-US')`, caseID, actor.ID)
				method, path, body := http.MethodPost, "/messages", `{"body":"Operator reply to the order case.","expectedVersion":1}`
				if action == "resolve" {
					method, path, body = http.MethodPatch, "", `{"status":"resolved","resolutionCode":"refund_guidance","expectedVersion":1}`
				}
				req, err := http.NewRequestWithContext(ctx, method, server.URL+"/api/v1/admin/support/cases/"+caseID.String()+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				type result struct {
					status int
					err    error
				}
				done := make(chan result, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					res, err := client.Do(req)
					if err != nil {
						done <- result{err: err}
						return
					}
					defer res.Body.Close()
					_, err = io.Copy(io.Discard, res.Body)
					done <- result{res.StatusCode, err}
				}()
				select {
				case <-entered:
				case res := <-done:
					t.Fatalf("request did not reach mutation gate: %+v", res)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				switch revoke {
				case "role":
					exec(`UPDATE users SET role='member' WHERE id=$1`, actor.ID)
				case "status":
					exec(`UPDATE users SET status='suspended' WHERE id=$1`, actor.ID)
				case "permission":
					exec(`DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:support'`)
				}
				release()
				select {
				case res := <-done:
					if res.err != nil || res.status != http.StatusForbidden {
						t.Fatalf("revoked operator mutation: %+v", res)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var unchanged bool
				if err := pool.QueryRow(ctx, `SELECT status='open' AND version=1 AND assigned_operator_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM support_messages WHERE case_id=$1)
 AND NOT EXISTS(SELECT 1 FROM support_events WHERE case_id=$1)
 AND NOT EXISTS(SELECT 1 FROM notifications WHERE resource_id=$1)
 FROM support_cases WHERE id=$1`, caseID).Scan(&unchanged); err != nil || !unchanged {
					t.Fatalf("denied command wrote effects: %v %v", unchanged, err)
				}
			})
		}
	}
}
