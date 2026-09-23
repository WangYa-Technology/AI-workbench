package community_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/jackc/pgx/v5/pgxpool"
)

func communityActors(t *testing.T, pool *pgxpool.Pool, count int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, count)
	for i := range ids {
		ids[i] = uuid.New()
		_, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Community review','member','active')`, ids[i], ids[i].String()+"@test.local", "c_"+strings.ReplaceAll(ids[i].String(), "-", "")[:20])
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func TestCommunityUnicodeIdempotencyAndAuthorLifecycle(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 2)
	repo := community.NewRepository(pool)
	input := community.PostCreateInput{Title: strings.Repeat("界", 120), Body: strings.Repeat("🙂", 2000), Category: "community_general"}
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post, err := repo.CreatePost(ctx, users[0], input, "parallel-post-key")
			ids <- post.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := <-ids
	if other := <-ids; other != id {
		t.Fatal("retry created two posts")
	}
	input.Title = "Different payload"
	if _, err := repo.CreatePost(ctx, users[0], input, "parallel-post-key"); !errors.Is(err, community.ErrConflict) {
		t.Fatalf("mismatched retry: %v", err)
	}
	if _, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "category", Body: "body", Category: "market_asset"}); !errors.Is(err, community.ErrInvalid) {
		t.Fatalf("category mapping: %v", err)
	}
	comment, err := repo.CreateComment(ctx, users[1], id, strings.Repeat("🙂", 1000), "comment-retry-key")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateComment(ctx, users[1], id, strings.Repeat("🙂", 1000), "comment-retry-key")
	if err != nil || replay.ID != comment.ID {
		t.Fatalf("comment replay: %v", err)
	}
	draft, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Draft: true, Title: "", Body: ""}, "private-draft-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetPostForViewer(ctx, users[1], draft.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("public draft: %v", err)
	}
	if _, err = repo.GetOwnedPost(ctx, users[1], draft.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("cross-user draft: %v", err)
	}
	empty, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Draft: true}, "delete-empty-draft")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.DeletePost(ctx, users[0], empty.ID, empty.Version); err != nil {
		t.Fatalf("delete empty draft: %v", err)
	}

	update := community.PostUpdateInput{PostCreateInput: community.PostCreateInput{Title: "Published draft", Body: "A complete discussion", Category: "community_general"}, ExpectedVersion: draft.Version}
	published, err := repo.UpdatePost(ctx, users[0], draft.ID, update)
	if err != nil || published.Status != "published" || published.Version != draft.Version+1 {
		t.Fatalf("publish draft: %#v %v", published, err)
	}
	if _, err = repo.UpdatePost(ctx, users[0], draft.ID, update); !errors.Is(err, community.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if err = repo.DeletePost(ctx, users[1], draft.ID, published.Version); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("cross-user deletion: %v", err)
	}
	if err = repo.DeletePost(ctx, users[0], draft.ID, published.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetPostForViewer(ctx, users[0], draft.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatal("owner-deleted post public")
	}
	if _, err = repo.CreateComment(ctx, users[1], draft.ID, "No longer allowed"); !errors.Is(err, community.ErrNotFound) {
		t.Fatal("comment on removed post")
	}
}

func TestCommunityConsistentVisibility(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 2)
	repo := community.NewRepository(pool)
	asset := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image','Source','/media/source.png','image/png','clean','delivery')`, asset, users[0]); err != nil {
		t.Fatal(err)
	}
	published, err := repo.Publish(ctx, users[0], community.PublishInput{AssetID: asset, Title: "Visible work", AIDisclosure: "Generated with consent", Body: "Public discussion"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateComment(ctx, users[1], published.PostID, "Public comment"); err != nil {
		t.Fatal(err)
	}
	assertInvisible := func() {
		t.Helper()
		if _, err := repo.GetPostForViewer(ctx, users[1], published.PostID); !errors.Is(err, community.ErrNotFound) {
			t.Fatalf("detail exposed: %v", err)
		}
		comments, err := repo.ListComments(ctx, published.PostID, community.CommentListInput{})
		if err != nil || len(comments.Items) != 0 {
			t.Fatalf("comments exposed: %#v %v", comments, err)
		}
		if _, err = repo.CreateComment(ctx, users[1], published.PostID, "Attempted comment"); !errors.Is(err, community.ErrNotFound) {
			t.Fatalf("comment accepted: %v", err)
		}
		if _, err = repo.SetReaction(ctx, users[1], published.PostID, "like", true); !errors.Is(err, community.ErrNotFound) {
			t.Fatalf("reaction accepted: %v", err)
		}
		if _, err = repo.ReportPost(ctx, users[1], published.PostID, community.ReportInput{Category: "other", Details: "Report inaccessible content"}); !errors.Is(err, community.ErrNotFound) {
			t.Fatalf("report accepted: %v", err)
		}
	}
	for _, statement := range []string{`UPDATE assets SET scan_status='review' WHERE id=$1`, `UPDATE assets SET scan_status='rejected' WHERE id=$1`} {
		if _, err = pool.Exec(ctx, statement, asset); err != nil {
			t.Fatal(err)
		}
		assertInvisible()
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, asset); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE works SET status='hidden' WHERE id=$1`, published.WorkID); err != nil {
		t.Fatal(err)
	}
	assertInvisible()
	if _, err = pool.Exec(ctx, `UPDATE works SET status='published' WHERE id=$1`, published.WorkID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, users[0]); err != nil {
		t.Fatal(err)
	}
	assertInvisible()
}

func TestCommunityAppealCannotOverrideNewerContentDecision(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 3)
	repo := community.NewRepository(pool)
	ops := admin.NewService(pool, false)
	asset := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type) VALUES($1,$2,'image','Source','/media/source.png','image/png','clean','delivery')`, asset, users[0]); err != nil {
		t.Fatal(err)
	}
	publication, err := repo.Publish(ctx, users[0], community.PublishInput{AssetID: asset, Title: "Protected content", Body: "Discussion evidence", AIDisclosure: "Generated with permission"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.ReportPost(ctx, users[1], publication.PostID, community.ReportInput{Category: "other", Details: "Evidence requiring a moderation decision"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveReport(ctx, users[2], report.ID, admin.ReportResolution{Outcome: "hidden", Reason: "Reviewed the first report evidence", Confirm: true, ExpectedVersion: 1}, "first-report"); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = pool.QueryRow(ctx, `SELECT version FROM works WHERE id=$1`, publication.WorkID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	input := admin.ContentUpdate{Status: "removed", Reason: "Separate review requires a newer restriction", Confirm: true, ExpectedVersion: version}
	if _, err = ops.UpdateContent(ctx, users[2], publication.WorkID, input, "newer-content"); err != nil {
		t.Fatal(err)
	}
	if _, err = ops.UpdateContent(ctx, users[2], publication.WorkID, input, "stale-content"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale direct change: %v", err)
	}
	appeal, err := repo.CreateAppeal(ctx, users[0], report.ID, "Evidence about the original report decision")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveAppeal(ctx, users[2], appeal.ID, admin.AppealResolution{Decision: "upheld", Reason: "Reconsider the earlier report only", Confirm: true, ExpectedVersion: 1}, "old-appeal"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("overwrote newer moderation: %v", err)
	}
	if _, err = repo.GetPostForViewer(ctx, users[1], publication.PostID); !errors.Is(err, community.ErrNotFound) {
		t.Fatal("newer moderation was undone")
	}
}

func TestCommunityAppealIsolationAndOverlappingHolds(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 4)
	repo := community.NewRepository(pool)
	ops := admin.NewService(pool, true)
	post, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "Moderated topic", Body: "Discussion evidence"})
	if err != nil {
		t.Fatal(err)
	}
	reports := make([]community.Report, 2)
	for i := range reports {
		reports[i], err = repo.ReportPost(ctx, users[i+1], post.ID, community.ReportInput{Category: "other", Details: "Private reporter evidence"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = ops.ResolveReport(ctx, users[3], reports[0].ID, admin.ReportResolution{Outcome: "hidden"}, "missing-reason"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("unchecked decision accepted: %v", err)
	}
	reason := "Reviewed evidence and temporarily restricted this discussion."
	decision := admin.ReportResolution{Outcome: "hidden", Reason: reason, Confirm: true, ExpectedVersion: 1}
	for _, report := range reports {
		resolved, e := ops.ResolveReport(ctx, users[3], report.ID, decision, "review-request")
		if e != nil || resolved.ResolutionReason == nil || *resolved.ResolutionReason != reason {
			t.Fatalf("resolve: %#v %v", resolved, e)
		}
	}
	var audit int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='community.report_resolved' AND request_id='review-request' AND reason=$1`, reason).Scan(&audit); err != nil || audit != 2 {
		t.Fatalf("audit: %d %v", audit, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE governance_events SET reason='changed' WHERE report_id=$1`, reports[0].ID); err == nil {
		t.Fatal("mutable governance evidence")
	}
	first, err := repo.CreateAppeal(ctx, users[0], reports[0].ID, "Author submitted additional evidence")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateAppeal(ctx, users[1], reports[0].ID, "Reporter submitted additional evidence")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users[:2] {
		page, e := repo.ListMyReports(ctx, user, community.ReportListInput{Limit: 1})
		if e != nil {
			t.Fatal(e)
		}
		_ = page
		item, e := repo.GetReportForViewer(ctx, user, reports[0].ID)
		if e != nil || item.Appeal == nil || item.Appeal.AppellantID != user {
			t.Fatalf("appeal isolation: %#v %v", item, e)
		}
		if user == users[0] && (item.ReporterID != uuid.Nil || item.Details != "") {
			t.Fatal("reporter privacy leaked")
		}
	}
	if _, err = repo.GetReportForViewer(ctx, users[3], reports[0].ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatalf("third-party case access: %v", err)
	}
	appealDecision := admin.AppealResolution{Decision: "upheld", Reason: "New evidence supports removing this restriction.", Confirm: true, ExpectedVersion: 1}
	if _, err = ops.ResolveAppeal(ctx, users[3], first.ID, appealDecision, "appeal-one"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetPostForViewer(ctx, users[0], post.ID); !errors.Is(err, community.ErrNotFound) {
		t.Fatal("one appeal removed another case's hold")
	}
	// The second party's appeal remains independently actionable.
	appealDecision.Decision = "upheld"
	if _, err = ops.ResolveAppeal(ctx, users[3], second.ID, appealDecision, "appeal-two"); err != nil {
		t.Fatal(err)
	}
	var caseStatus string
	if err = pool.QueryRow(ctx, `SELECT status FROM content_reports WHERE id=$1`, reports[0].ID).Scan(&caseStatus); err != nil || caseStatus != "dismissed" {
		t.Fatalf("second appeal reopened a reversed sanction: %s %v", caseStatus, err)
	}
	last, err := repo.CreateAppeal(ctx, users[0], reports[1].ID, "Evidence also resolves the remaining restriction")
	if err != nil {
		t.Fatal(err)
	}
	appealDecision.Decision = "upheld"
	if _, err = ops.ResolveAppeal(ctx, users[3], last.ID, appealDecision, "appeal-last"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetPostForViewer(ctx, users[0], post.ID); err != nil {
		t.Fatal("last hold did not restore post", err)
	}
}

func TestCommunityFeedSnapshotsAndPersonalViews(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 2)
	repo := community.NewRepository(pool)
	var posts []community.Post
	for range 25 {
		post, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "Snapshot discussion", Body: "A public discussion", Category: "community_general"})
		if err != nil {
			t.Fatal(err)
		}
		posts = append(posts, post)
	}
	page, err := repo.ListPageForViewer(ctx, users[1], community.PostListInput{Sort: "discussed", Limit: 10})
	if err != nil || page.Total != 25 || page.NextCursor == nil {
		t.Fatalf("snapshot start: %#v %v", page, err)
	}
	// Move the oldest item to the top of a live ranking after the first page.
	if _, err = repo.CreateComment(ctx, users[1], posts[0].ID, "Ranking changed after snapshot"); err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, post := range page.Items {
		seen[post.ID] = true
	}
	for page.NextCursor != nil {
		page, err = repo.ListPageForViewer(ctx, users[1], community.PostListInput{Sort: "discussed", Limit: 10, Cursor: *page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, post := range page.Items {
			if seen[post.ID] {
				t.Fatal("duplicate rank")
			}
			seen[post.ID] = true
		}
	}
	if len(seen) != 25 {
		t.Fatal("rank changes lost posts", len(seen))
	}
	initial, err := repo.ListPageForViewer(ctx, users[1], community.PostListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ListPageForViewer(ctx, users[0], community.PostListInput{Cursor: *initial.NextCursor, Limit: 1}); !errors.Is(err, community.ErrInvalidPostFilter) {
		t.Fatal("cross-user cursor")
	}
	if _, err = repo.ListPageForViewer(ctx, users[1], community.PostListInput{Cursor: *initial.NextCursor, Query: "different", Limit: 1}); !errors.Is(err, community.ErrInvalidPostFilter) {
		t.Fatal("cross-filter cursor")
	}
	if _, err = repo.SetReaction(ctx, users[1], posts[0].ID, "bookmark", true); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.ListPageForViewer(ctx, users[1], community.PostListInput{View: "saved"})
	if err != nil || saved.Total != 1 || saved.Items[0].ID != posts[0].ID {
		t.Fatalf("saved discussion: %#v %v", saved, err)
	}
	if _, err = repo.SetFollow(ctx, users[1], users[0], true); err != nil {
		t.Fatal(err)
	}
	following, err := repo.ListPageForViewer(ctx, users[1], community.PostListInput{View: "following"})
	if err != nil || following.Total != 25 {
		t.Fatalf("following: %#v %v", following, err)
	}
}

func TestCommunityAppealCannotRestoreOwnerDeletion(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 3)
	repo := community.NewRepository(pool)
	ops := admin.NewService(pool, true)
	post, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "Author withdrawal", Body: "This discussion may be withdrawn"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.ReportPost(ctx, users[1], post.ID, community.ReportInput{Category: "other", Details: "Review this discussion carefully"})
	if err != nil {
		t.Fatal(err)
	}
	decision := admin.ReportResolution{Outcome: "hidden", Reason: "Review found a temporary restriction necessary.", Confirm: true, ExpectedVersion: 99}
	if _, err = ops.ResolveReport(ctx, users[2], report.ID, decision, "stale-decision"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}
	decision.ExpectedVersion = 1
	if _, err = ops.ResolveReport(ctx, users[2], report.ID, decision, "valid-decision"); err != nil {
		t.Fatal(err)
	}
	owned, err := repo.GetOwnedPost(ctx, users[0], post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.DeletePost(ctx, users[0], post.ID, owned.Version); err != nil {
		t.Fatal(err)
	}
	appeal, err := repo.CreateAppeal(ctx, users[0], report.ID, "Please reconsider the moderation evidence")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveAppeal(ctx, users[2], appeal.ID, admin.AppealResolution{Decision: "upheld", Reason: "Evidence supports lifting this restriction.", Confirm: true, ExpectedVersion: 1}, "unsafe-restore"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("restored owner-deleted content: %v", err)
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT status FROM posts WHERE id=$1`, post.ID).Scan(&state); err != nil || state != "removed" {
		t.Fatalf("deletion lost: %s %v", state, err)
	}
}

func TestCommunityUpheldNoActionReopensReviewAndNewAppealRound(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 3)
	repo := community.NewRepository(pool)
	ops := admin.NewService(pool, true)
	post, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "Review new evidence", Body: "The reporter can request reconsideration"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.ReportPost(ctx, users[1], post.ID, community.ReportInput{Category: "other", Details: "Initial evidence for content review"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ops.ResolveReport(ctx, users[2], report.ID, admin.ReportResolution{Outcome: "no_action", Reason: "Insufficient evidence for a restriction.", Confirm: true, ExpectedVersion: 1}, "first-review")
	if err != nil {
		t.Fatal(err)
	}
	appeal, err := repo.CreateAppeal(ctx, users[1], report.ID, "Additional evidence now supports another review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveAppeal(ctx, users[2], appeal.ID, admin.AppealResolution{Decision: "upheld", Reason: "New evidence warrants reopening the review.", Confirm: true, ExpectedVersion: 1}, "reopen-review"); err != nil {
		t.Fatal(err)
	}
	caseItem, err := repo.GetReportForViewer(ctx, users[1], report.ID)
	if err != nil || caseItem.Status != "reviewing" {
		t.Fatalf("reopening not actionable: %#v %v", caseItem, err)
	}
	if _, err = ops.ResolveReport(ctx, users[2], report.ID, admin.ReportResolution{Outcome: "hidden", Reason: "The newly reviewed evidence supports restriction.", Confirm: true, ExpectedVersion: resolved.Version + 1}, "second-review"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateAppeal(ctx, users[1], report.ID, "The new decision has its own appeal opportunity"); err != nil {
		t.Fatal("new decision blocked by prior appeal", err)
	}
	var notifications int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE resource_type='content_report' AND resource_id=$1 AND target_path=$2`, report.ID, "/community/reports/"+report.ID.String()).Scan(&notifications); err != nil || notifications != 6 {
		t.Fatalf("each round must notify both parties: %d %v", notifications, err)
	}
}

func TestCommunityCommentGovernanceRestoresOnlyVisibleParent(t *testing.T) {
	pool, cleanup := governanceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	users := communityActors(t, pool, 3)
	repo := community.NewRepository(pool)
	ops := admin.NewService(pool, false)
	post, err := repo.CreatePost(ctx, users[0], community.PostCreateInput{Title: "Comment governance", Body: "Parent discussion"})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := repo.CreateComment(ctx, users[1], post.ID, "Review this comment with evidence")
	if err != nil {
		t.Fatal(err)
	}
	report := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO content_reports(id,reporter_id,resource_type,resource_id,subject_author_id,category,details) VALUES($1,$2,'comment',$3,$4,'other','Independent comment review evidence')`, report, users[0], comment.ID, users[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveReport(ctx, users[2], report, admin.ReportResolution{Outcome: "hidden", Reason: "Comment evidence warrants temporary hiding", Confirm: true, ExpectedVersion: 1}, "comment-report"); err != nil {
		t.Fatal(err)
	}
	appeal, err := repo.CreateAppeal(ctx, users[1], report, "Comment source evidence supports restoration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE posts SET status='hidden' WHERE id=$1`, post.ID); err != nil {
		t.Fatal(err)
	}
	decision := admin.AppealResolution{Decision: "upheld", Reason: "Reviewed the comment source evidence", Confirm: true, ExpectedVersion: 1}
	if _, err = ops.ResolveAppeal(ctx, users[2], appeal.ID, decision, "hidden-parent"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("hidden parent restored comment: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE posts SET status='published' WHERE id=$1`, post.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = ops.ResolveAppeal(ctx, users[2], appeal.ID, decision, "visible-parent"); err != nil {
		t.Fatal(err)
	}
	comments, err := repo.ListComments(ctx, post.ID, community.CommentListInput{})
	if err != nil || len(comments.Items) != 1 {
		t.Fatalf("comment restoration failed: %v", err)
	}
}
