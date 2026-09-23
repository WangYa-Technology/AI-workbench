package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

func inspirationExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func inspirationWork(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, title string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	asset, work, post := uuid.New(), uuid.New(), uuid.New()
	inspirationExec(t, pool, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,storage_backend,storage_key,license_code) VALUES($1,$2,'image',$3,$4,'image/jpeg','clean','upload','local_file',$1::uuid::text||'.jpg','personal')`, asset, author, title, "/api/v1/assets/"+asset.String()+"/content")
	inspirationExec(t, pool, `INSERT INTO works(id,author_id,asset_id,title,summary,prompt,prompt_visibility,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,$4,'Reference study','private secret','private','Test','published','AI assisted artwork',now())`, work, author, asset, title)
	inspirationExec(t, pool, `INSERT INTO posts(id,author_id,work_id,body,status,published_at) VALUES($1,$2,$3,'Discuss this reference','published',now())`, post, author, work)
	return work, asset, post
}
func inspirationServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.jpg"), []byte("image content"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	return server
}

func TestInspirationVisibilityInteractionsAndSourceBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := inspirationServer(t, pool)
	owner, viewer, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	author := registerGovernanceUser(t, owner, server.URL, "inspiration_owner")
	viewerUser := registerGovernanceUser(t, viewer, server.URL, "inspiration_viewer")
	work, asset, post := inspirationWork(t, pool, author.ID, "Visibility reference")
	inspirationExec(t, pool, `UPDATE assets SET storage_key='image.jpg' WHERE id=$1`, asset)
	base := server.URL + "/api/v1"
	get := func(client *http.Client, path string, want int, out any) {
		t.Helper()
		if res := requestJSON(t, client, "GET", base+path, nil, out); res.StatusCode != want {
			t.Fatalf("GET %s: %d want %d", path, res.StatusCode, want)
		}
	}
	var detail discovery.Work
	get(guest, "/works/"+work.String(), 200, &detail)
	if detail.PostID == nil || *detail.PostID != post || detail.Prompt != nil || detail.ViewerBookmarked {
		t.Fatal("unsafe work projection", detail)
	}
	if res := requestJSON(t, viewer, "PUT", base+"/community/posts/"+post.String()+"/reactions/bookmark", map[string]any{"active": true}, nil); res.StatusCode != 200 {
		t.Fatal("bookmark failed")
	}
	get(viewer, "/works/"+work.String(), 200, &detail)
	if !detail.ViewerBookmarked {
		t.Fatal("missing viewer bookmark")
	}
	detail = discovery.Work{}
	get(guest, "/works/"+work.String(), 200, &detail)
	if detail.ViewerBookmarked {
		t.Fatal("viewer state leaked")
	}
	var saved assets.SavedWorkPage
	get(viewer, "/assets/saved-works", 200, &saved)
	if len(saved.Items) != 1 {
		t.Fatal("saved work missing")
	}
	get(guest, "/assets/"+asset.String()+"/content", 200, nil)
	for _, state := range []string{"suspended", "deleted"} {
		inspirationExec(t, pool, `UPDATE users SET status=$2 WHERE id=$1`, author.ID, state)
		get(guest, "/works/"+work.String(), 404, nil)
		get(guest, "/assets/"+asset.String()+"/content", 403, nil)
		get(guest, "/community/posts/"+post.String(), 404, nil)
		var page discovery.Page
		get(guest, "/works", 200, &page)
		if page.Total != 0 || len(page.Items) != 0 || len(page.CategoryCounts) != 0 {
			t.Fatal("inactive author visible")
		}
		get(guest, "/creators/"+author.Handle, 404, nil)
		saved = assets.SavedWorkPage{}
		get(viewer, "/assets/saved-works", 200, &saved)
		if len(saved.Items) != 0 {
			t.Fatal("invisible bookmark leaked")
		}
		if res := requestJSON(t, viewer, "POST", base+"/generations", map[string]any{"mode": "image", "prompt": "Use reference safely", "sourceWorkId": work}, nil); res.StatusCode != 422 {
			t.Fatalf("inactive source allowed: %d", res.StatusCode)
		}
	}
	failed := uuid.New()
	inspirationExec(t, pool, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,source_work_id) VALUES($1,$2,'image','local','Test','attribution retry','failed',$3)`, failed, viewerUser.ID, work)
	service := creation.NewService(pool, t.TempDir(), "", true)
	if _, err := service.Retry(context.Background(), viewerUser.ID, failed, "source-retry-001", "test"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("retry accepted invisible source: %v", err)
	}
	if res := requestJSON(t, viewer, "POST", base+"/generations", map[string]any{"mode": "image", "prompt": "Use reference safely", "sourceWorkId": uuid.New()}, nil); res.StatusCode != 422 {
		t.Fatalf("nonexistent source allowed: %d", res.StatusCode)
	}

	inspirationExec(t, pool, `UPDATE users SET status='active' WHERE id=$1`, author.ID)
	for _, update := range []string{"UPDATE works SET status='hidden' WHERE id=$1", "UPDATE assets SET scan_status='review' WHERE id=$1"} {
		id := work
		if strings.Contains(update, "assets") {
			id = asset
		}
		inspirationExec(t, pool, update, id)
		get(guest, "/works/"+work.String(), 404, nil)
		if res := requestJSON(t, viewer, "POST", base+"/generations", map[string]any{"mode": "image", "prompt": "Use reference safely", "sourceWorkId": work}, nil); res.StatusCode != 422 {
			t.Fatalf("invisible source allowed: %d", res.StatusCode)
		}
		inspirationExec(t, pool, `UPDATE works SET status='published' WHERE id=$1`, work)
	}
	inspirationExec(t, pool, `UPDATE assets SET scan_status='clean' WHERE id=$1`, asset)
	inspirationExec(t, pool, `UPDATE works SET status='hidden' WHERE id=$1`, work)
	// Hiding a public work does not revoke the owner's independent media access.
	get(owner, "/assets/"+asset.String()+"/content", 200, nil)
	get(guest, "/assets/"+asset.String()+"/content", 403, nil)
	// A second visible work independently grants public access to the same asset.
	otherWork := uuid.New()
	inspirationExec(t, pool, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at) VALUES($1,$2,$3,'Second public use','','Test','published','AI assisted artwork',now())`, otherWork, author.ID, asset)
	get(guest, "/assets/"+asset.String()+"/content", 200, nil)
	// Clean derivatives must still be hidden while their origin is under review.
	_, origin, _ := inspirationWork(t, pool, author.ID, "Origin under review")
	inspirationExec(t, pool, `UPDATE assets SET origin_asset_id=$2 WHERE id=$1`, asset, origin)
	inspirationExec(t, pool, `UPDATE assets SET scan_status='review' WHERE id=$1`, origin)
	get(guest, "/works/"+otherWork.String(), 404, nil)
	get(guest, "/assets/"+asset.String()+"/content", 404, nil)

}

func TestInspirationReferencesPromptsAndCreatorPagination(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := inspirationServer(t, pool)
	client := testHTTPClient(t)
	user := registerGovernanceUser(t, client, server.URL, "portfolio_author")
	base := server.URL + "/api/v1"
	// Active accounts have a public identity even without works/products.
	var empty discovery.CreatorProfile
	if res := requestJSON(t, client, "GET", base+"/creators/"+user.Handle, nil, &empty); res.StatusCode != 200 || empty.WorksTotal != 0 {
		t.Fatal("empty creator inaccessible")
	}
	source, _, _ := inspirationWork(t, pool, user.ID, "Original private prompt")
	derived, asset, _ := inspirationWork(t, pool, user.ID, "Derived public work")
	inspirationExec(t, pool, `INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,output_asset_id,source_work_id) VALUES($1,'image','local','Test','do not expose','succeeded',$2,$3)`, user.ID, asset, source)
	var work discovery.Work
	if res := requestJSON(t, client, "GET", base+"/works/"+derived.String(), nil, &work); res.StatusCode != 200 || len(work.Sources) != 1 || work.Sources[0].ID != source || work.Prompt != nil {
		t.Fatal("source projection failed", work)
	}
	inspirationExec(t, pool, `UPDATE works SET status='hidden' WHERE id=$1`, source)
	work = discovery.Work{}
	requestJSON(t, client, "GET", base+"/works/"+derived.String(), nil, &work)
	if len(work.Sources) != 0 {
		t.Fatal("hidden source leaked")
	}
	inspirationExec(t, pool, `UPDATE works SET status='published' WHERE id=$1`, source)
	for _, visibility := range []string{"private", "partial", "purchased", "public"} {
		inspirationExec(t, pool, `UPDATE works SET prompt_visibility=$2 WHERE id=$1`, source, visibility)
		work = discovery.Work{}
		requestJSON(t, client, "GET", base+"/works/"+source.String(), nil, &work)
		if (work.Prompt != nil) != (visibility == "public") {
			t.Fatalf("prompt leaked for %s", visibility)
		}
	}
	for _, visibility := range []string{"partial", "purchased"} {
		input := map[string]any{"assetId": asset, "title": "Unsupported prompt mode", "summary": "", "prompt": "secret", "promptVisibility": visibility, "aiDisclosure": "AI assisted artwork", "body": ""}
		if res := requestJSON(t, client, "POST", base+"/publications", input, nil); res.StatusCode != 422 {
			t.Fatalf("unsupported publish mode %s: %d", visibility, res.StatusCode)
		}
		if res := requestJSON(t, client, "POST", base+"/content-drafts", input, nil); res.StatusCode != 422 {
			t.Fatalf("unsupported draft mode %s: %d", visibility, res.StatusCode)
		}
	}

	for i := 0; i < 25; i++ {
		inspirationWork(t, pool, user.ID, fmt.Sprintf("Portfolio %02d", i))
	}
	productAsset := uuid.New()
	inspirationExec(t, pool, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
	 VALUES($1,$2,'image','Private portfolio product','/media/private.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, productAsset, user.ID)
	for i := 0; i < 26; i++ {
		inspirationExec(t, pool, `INSERT INTO products(seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility) VALUES($1,$2,$3,'Portfolio product','asset',100,'USD','hcai-commercial-standard-v1','active','AI assisted artwork','[]','HCAI')`, user.ID, productAsset, fmt.Sprintf("Product %02d", i))
	}
	seenWorks, seenProducts := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for page := 1; page <= 3; page++ {
		var profile discovery.CreatorProfile
		res := requestJSON(t, client, "GET", fmt.Sprintf("%s/creators/%s?worksPage=%d&productsPage=%d", base, user.Handle, page, page), nil, &profile)
		if res.StatusCode != 200 || profile.WorksTotal != 27 || profile.ProductsTotal != 26 {
			t.Fatalf("creator totals/page: %d %#v", res.StatusCode, profile)
		}
		for _, w := range profile.Works {
			if seenWorks[w.ID] {
				t.Fatal("duplicate work")
			}
			seenWorks[w.ID] = true
		}
		for _, p := range profile.Products {
			if seenProducts[p.ID] {
				t.Fatal("duplicate product")
			}
			seenProducts[p.ID] = true
		}
	}
	if len(seenWorks) != 27 || len(seenProducts) != 26 {
		t.Fatal("portfolio truncated")
	}
	inspirationExec(t, pool, `UPDATE licenses SET status='retired' WHERE code='hcai-commercial-standard-v1'`)
	var profile discovery.CreatorProfile
	res := requestJSON(t, client, "GET", base+"/creators/"+user.Handle, nil, &profile)
	if res.StatusCode != 200 || profile.ProductsTotal != 0 || len(profile.Products) != 0 {
		t.Fatal("inactive license exposed")
	}
}

func TestInspirationFilterValidationUnicodeAndLiteralSearch(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := inspirationServer(t, pool)
	client := testHTTPClient(t)
	user := registerGovernanceUser(t, client, server.URL, "literal_author")
	base := server.URL + "/api/v1"
	inspirationWork(t, pool, user.ID, "100%_literal")
	inspirationWork(t, pool, user.ID, "100xxliteral")
	for _, query := range []string{"%_", strings.Repeat("界", 120), strings.Repeat("🙂", 120)} {
		var page discovery.Page
		res := requestJSON(t, client, "GET", base+"/works?q="+url.QueryEscape(query), nil, &page)
		if res.StatusCode != 200 {
			t.Fatal("unicode query rejected")
		}
		if query == "%_" && page.Total != 1 {
			t.Fatal("wildcards were not literal")
		}
		var search discovery.SearchPage
		res = requestJSON(t, client, "GET", base+"/search?q="+url.QueryEscape(query), nil, &search)
		if res.StatusCode != 200 {
			t.Fatal("unicode search rejected")
		}
		if query == "%_" && search.Total != 1 {
			t.Fatal("search wildcards were not literal")
		}
	}
	for _, path := range []string{"/works?kind=bogus", "/works?promptVisibility=bogus", "/works?limit=0", "/works?limit=25", "/works?limit=abc", "/works?q=" + url.QueryEscape(strings.Repeat("🙂", 121)), "/search?q=okay&page=0", "/search?q=okay&limit=abc", "/search?q=" + url.QueryEscape(strings.Repeat("界", 121)), "/creators/" + user.Handle + "?worksPage=-1"} {
		if res := requestJSON(t, client, "GET", base+path, nil, nil); res.StatusCode != 422 {
			t.Fatalf("invalid query accepted %s: %d", path, res.StatusCode)
		}
	}
	var page discovery.Page
	requestJSON(t, client, "GET", base+"/works?limit=1", nil, &page)
	if page.NextCursor == nil || page.Total != 2 {
		t.Fatal("missing pagination/total")
	}
	if res := requestJSON(t, client, "GET", base+"/works?kind=image&cursor="+url.QueryEscape(*page.NextCursor), nil, nil); res.StatusCode != 400 {
		t.Fatal("cross-filter cursor accepted")
	}
}

func TestInspirationCountsShareReadSnapshot(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	author := uuid.New()
	inspirationExec(t, pool, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,'snapshot@test.local','snapshot_author','Snapshot author','creator','active')`, author)
	work, _, _ := inspirationWork(t, pool, author, "Snapshot work")
	repo := discovery.NewRepository(pool)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := pool.Exec(ctx, `UPDATE works SET status=CASE WHEN status='published' THEN 'hidden' ELSE 'published' END WHERE id=$1`, work); err != nil {
				errs <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
		select {
		case err := <-errs:
			t.Error(err)
		default:
		}
	}()
	for i := 0; i < 30; i++ {
		page, err := repo.List(ctx, 12, nil, discovery.WorkFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != len(page.Items) || page.CategoryCounts["image"] != page.Total {
			t.Fatalf("mixed snapshots: %#v", page)
		}
	}
}

func TestInspirationLineageStopsAtInvisibleSourcesAndCycles(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := inspirationServer(t, pool)
	client := testHTTPClient(t)
	author := registerGovernanceUser(t, client, server.URL, "lineage_old")
	other := registerGovernanceUser(t, client, server.URL, "lineage_new")
	original, originalAsset, _ := inspirationWork(t, pool, author.ID, "First reference")
	middle, middleAsset, _ := inspirationWork(t, pool, other.ID, "Second reference")
	final, finalAsset, _ := inspirationWork(t, pool, other.ID, "Third reference")
	for _, link := range [][2]uuid.UUID{{middleAsset, original}, {finalAsset, middle}, {originalAsset, final}} {
		inspirationExec(t, pool, `INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,output_asset_id,source_work_id) VALUES($1,'image','local','Test','never expose this','succeeded',$2,$3)`, other.ID, link[0], link[1])
	}
	read := func() discovery.Work {
		t.Helper()
		var work discovery.Work
		if res := requestJSON(t, client, "GET", server.URL+"/api/v1/works/"+final.String(), nil, &work); res.StatusCode != 200 {
			t.Fatal("read derived work")
		}
		return work
	}
	work := read()
	if len(work.Sources) != 2 || work.Sources[0].ID != middle || work.Sources[1].ID != original {
		t.Fatal("lineage order or cycle handling", work.Sources)
	}
	inspirationExec(t, pool, `UPDATE users SET status='suspended' WHERE id=$1`, author.ID)
	if work = read(); len(work.Sources) != 1 || work.Sources[0].ID != middle {
		t.Fatal("inactive source author leaked")
	}
	inspirationExec(t, pool, `UPDATE users SET status='active' WHERE id=$1`, author.ID)
	for _, state := range []string{"hidden", "removed"} {
		inspirationExec(t, pool, `UPDATE works SET status=$2 WHERE id=$1`, middle, state)
		if work = read(); len(work.Sources) != 0 {
			t.Fatal("traversed hidden intermediate source")
		}
	}
	inspirationExec(t, pool, `UPDATE works SET status='published' WHERE id=$1`, middle)
	inspirationExec(t, pool, `UPDATE assets SET scan_status='review' WHERE id=$1`, originalAsset)
	if work = read(); len(work.Sources) != 1 {
		t.Fatal("unsafe source leaked")
	}
}
