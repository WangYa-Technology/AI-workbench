package main

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
)

const (
	demoCreatorID              = "00000000-0000-4000-8000-000000000001"
	demoAssetID                = "00000000-0000-4000-8000-000000000101"
	demoWorkID                 = "00000000-0000-4000-8000-000000000201"
	demoPostID                 = "00000000-0000-4000-8000-000000000301"
	demoOwnedAssetID           = "00000000-0000-4000-8000-000000000102"
	demoTaskVideoID            = "00000000-0000-4000-8000-000000000401"
	demoTaskImageID            = "00000000-0000-4000-8000-000000000402"
	demoTaskAudioID            = "00000000-0000-4000-8000-000000000403"
	demoProductAssetWorkflowID = "00000000-0000-4000-8000-000000000103"
	demoProductAssetPromptID   = "00000000-0000-4000-8000-000000000104"
	demoProductWorkID          = "00000000-0000-4000-8000-000000000501"
	demoProductWorkflowID      = "00000000-0000-4000-8000-000000000502"
	demoProductPromptID        = "00000000-0000-4000-8000-000000000503"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,locale,timezone)
		VALUES
			($1,'studio@demo.hcai.local','northstar_studio','Northstar Studio','creator','active','en-US','America/Los_Angeles'),
			($2,'creator@demo.hcai.local','maya_chen','Maya Chen','creator','active','en-US','America/New_York'),
			($3,'publisher@demo.hcai.local','arc_atlas','Arc Atlas','publisher','active','en-US','Europe/London'),
			($4,'operations@demo.hcai.local','hcai_operations','HCAI Operations','admin','active','en-US','UTC')
		ON CONFLICT (id) DO UPDATE SET
			email=excluded.email,handle=excluded.handle,display_name=excluded.display_name,role=excluded.role,
			status=excluded.status,locale=excluded.locale,timezone=excluded.timezone,updated_at=now()`,
		uuid.MustParse(demoCreatorID), uuid.MustParse(identity.DemoUserID), uuid.MustParse(identity.DemoPublisherID), uuid.MustParse(identity.DemoAdminID))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO notifications(id,user_id,kind,title,body,target_path,resource_type,resource_id,source_key,delivery_status,delivered_at,created_at)
		VALUES
		 ('00000000-0000-4000-8000-000000000701',$1,'task.proposal_accepted','Proposal accepted',
		  'Your proposal for “Modular album artwork for an electronic release” was accepted. The brief is ready in your task workspace.',
		  '/market/demands/00000000-0000-4000-8000-000000000402','task','00000000-0000-4000-8000-000000000402','seed:creator:proposal','delivered',now()-interval '12 minutes',now()-interval '12 minutes'),
		 ('00000000-0000-4000-8000-000000000702',$1,'generation.completed','Asset ready',
		  'A local test generation completed and is available in Assets with its source evidence.',
		  '/workspace/assets','generation',NULL,'seed:creator:generation','delivered',now()-interval '48 minutes',now()-interval '48 minutes'),
		 ('00000000-0000-4000-8000-000000000703',$2,'task.proposal_submitted','New proposal received',
		  'A creator submitted a proposal for “Generative runway film for a climate collection”.',
		  '/market/demands/00000000-0000-4000-8000-000000000401','task','00000000-0000-4000-8000-000000000401','seed:publisher:proposal','delivered',now()-interval '20 minutes',now()-interval '20 minutes')
		ON CONFLICT (user_id,source_key) WHERE source_key IS NOT NULL DO UPDATE SET
		  title=excluded.title,body=excluded.body,target_path=excluded.target_path,resource_type=excluded.resource_type,resource_id=excluded.resource_id`,
		uuid.MustParse(identity.DemoUserID), uuid.MustParse(identity.DemoPublisherID))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions(user_id,token_hash,expires_at,client_label)
		VALUES($1,$2,$3,'Seeded local admin session')
		ON CONFLICT (token_hash) DO UPDATE SET user_id=excluded.user_id,expires_at=excluded.expires_at,revoked_at=NULL`,
		uuid.MustParse(identity.DemoAdminID), identity.HashToken(identity.DemoAdminToken), time.Now().Add(30*24*time.Hour))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions(user_id,token_hash,expires_at)
		VALUES($1,$2,$5),($3,$4,$5)
		ON CONFLICT (token_hash) DO UPDATE SET user_id=excluded.user_id,expires_at=excluded.expires_at,revoked_at=NULL`,
		uuid.MustParse(identity.DemoUserID), identity.HashToken(identity.DemoToken),
		uuid.MustParse(identity.DemoPublisherID), identity.HashToken(identity.DemoPublisherToken), time.Now().Add(30*24*time.Hour))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Signal Architecture','/media/home-cinematic.jpg','image/jpeg',2000,2500,'clean','demo','demo-display-only'),
		      ($3,$4,'image','Creator delivery study','/media/home-cinematic.jpg','image/jpeg',2000,2500,'clean','demo','creator-owned-local-test'),
		      ($5,$2,'image','Architectural campaign workflow','/media/home-cinematic.jpg','image/jpeg',2000,2500,'clean','demo','hcai-commercial-standard-v1'),
		      ($6,$2,'image','Editorial architecture prompt system','/media/home-cinematic.jpg','image/jpeg',2000,2500,'clean','demo','hcai-personal-v1')
		ON CONFLICT (id) DO UPDATE SET title=excluded.title,media_url=excluded.media_url,scan_status='clean'`,
		uuid.MustParse(demoAssetID), uuid.MustParse(demoCreatorID), uuid.MustParse(demoOwnedAssetID), uuid.MustParse(identity.DemoUserID),
		uuid.MustParse(demoProductAssetWorkflowID), uuid.MustParse(demoProductAssetPromptID))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,
		 ai_disclosure,included_files,compatibility)
		VALUES
		 ($1,$5,$6,'Signal Architecture campaign license',
		  'A production-ready visual license for editorial campaigns that need a controlled architectural image with generous typography space.',
		  'work',3800,'USD','hcai-commercial-standard-v1','active',
		  'Demo source image prepared for local workflow verification. No production model or paid generation call is represented.',
		  '["2000x2500 JPEG master","Usage and attribution notes"]','JPEG; compatible with major image editors'),
		 ($2,$5,$7,'Architectural campaign workflow',
		  'A reusable composition workflow for creating restrained architectural campaign images with controlled light and negative space.',
		  'workflow',2400,'USD','hcai-commercial-standard-v1','active',
		  'Demo workflow and preview for local testing. The included output is deterministic demo media.',
		  '["Workflow specification","Prompt stages","2000x2500 preview"]','HCAI Image workspace; plain-text workflow included'),
		 ($3,$5,$8,'Editorial architecture prompt system',
		  'A structured prompt system for editorial architecture studies, including composition, light, material, and exclusion guidance.',
		  'prompt',1200,'USD','hcai-personal-v1','active',
		  'Demo prompt package for local testing. Output quality varies by model and no production model call is included.',
		  '["Master prompt","Three composition variants","Negative prompt guidance"]','Plain text; adaptable to image models'),
		 ($4,$5,$6,'Paused demo product','This product exists to verify that inactive listings never appear in the marketplace.',
		  'asset',900,'USD','hcai-personal-v1','paused','Demo only.','[]','')
		ON CONFLICT (id) DO UPDATE SET title=excluded.title,description=excluded.description,product_type=excluded.product_type,
		 price_cents=excluded.price_cents,license_code=excluded.license_code,status=excluded.status,
		 ai_disclosure=excluded.ai_disclosure,included_files=excluded.included_files,compatibility=excluded.compatibility,updated_at=now()`,
		uuid.MustParse(demoProductWorkID), uuid.MustParse(demoProductWorkflowID), uuid.MustParse(demoProductPromptID),
		uuid.MustParse("00000000-0000-4000-8000-000000000504"), uuid.MustParse(demoCreatorID),
		uuid.MustParse(demoAssetID), uuid.MustParse(demoProductAssetWorkflowID), uuid.MustParse(demoProductAssetPromptID))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,summary,brief,deliverable_type,deliverables,acceptance_rules,rights_terms,
		 ai_disclosure_requirement,budget_cents,currency,deadline,status,client_timezone,allow_direct_accept,idempotency_key)
		VALUES
		 ($1,$4,'Generative runway film for a climate collection',
		  'Create a refined 20-second fashion loop built around wind, recycled textiles, and controlled camera motion.',
		  'Arc Atlas is commissioning a launch film for an independent circular-fashion collection. The piece should feel tactile and editorial, keep the garments legible, and avoid synthetic skin artifacts. Supply a master and a social crop.',
		  'video','["20-second 4K master in 16:9","10-second vertical crop in 9:16","Prompt and model disclosure sheet"]',
		  '["Garment silhouette remains consistent across the sequence","No visible brand marks or third-party characters","Both exports are clean and playable"]',
		  'Worldwide commercial campaign use for 12 months. Creator retains portfolio display rights after launch.',
		  'List every generative model and disclose any source media used in the final sequence.',120000,'USD','2026-09-15T17:00:00Z','open','Europe/London',false,'seed-video-2026'),
		 ($2,$4,'Modular album artwork for an electronic release',
		  'Design a square cover system with one master composition and three color-safe release variants.',
		  'Build a visual identity for a four-track electronic release using architectural light, crisp typography-safe negative space, and a restrained material palette. The artwork must remain clear at streaming thumbnail size.',
		  'image','["3000x3000 master cover","Three alternate color treatments","Layered source or reproducible workflow"]',
		  '["Title-safe area remains unobstructed","No copyrighted logos or recognizable people","All variants share one coherent composition"]',
		  'Exclusive commercial release artwork license. Creator may show the work after the public release date.',
		  'Identify generated elements and provide the final prompt or workflow summary.',65000,'USD','2026-09-02T20:00:00Z','open','Europe/London',true,'seed-image-2026'),
		 ($3,$4,'Spatial audio identity for a research podcast',
		  'Produce a short sonic identity that works as an intro, transition, and clean instrumental bed.',
		  'The series covers urban systems and climate research. The sound should be precise, calm, and contemporary without imitating a known artist. Voice samples are not permitted. Deliver stems so the publisher can make accessible mixes.',
		  'audio','["12-second stereo intro","4-second transition","30-second loop and separated stems"]',
		  '["Integrated loudness is suitable for spoken-word use","No voice or uncleared samples","Loop has no audible seam"]',
		  'Perpetual commercial podcast use. No resale as a standalone sample pack.',
		  'Disclose generative music tools, source samples, and any manual post-production.',90000,'USD','2026-09-22T16:00:00Z','open','Europe/London',false,'seed-audio-2026')
		ON CONFLICT (id) DO UPDATE SET title=excluded.title,summary=excluded.summary,brief=excluded.brief,
		 deliverables=excluded.deliverables,acceptance_rules=excluded.acceptance_rules,rights_terms=excluded.rights_terms,
		 ai_disclosure_requirement=excluded.ai_disclosure_requirement,deadline=excluded.deadline,updated_at=now()`,
		uuid.MustParse(demoTaskVideoID), uuid.MustParse(demoTaskImageID), uuid.MustParse(demoTaskAudioID), uuid.MustParse(identity.DemoPublisherID))
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO task_events(demand_id,actor_id,kind,to_status,note)
		SELECT d.id,d.client_id,'created','open','Seeded local test brief'
		FROM demands d
		WHERE d.id=ANY($1) AND NOT EXISTS(SELECT 1 FROM task_events e WHERE e.demand_id=d.id)`,
		[]uuid.UUID{uuid.MustParse(demoTaskVideoID), uuid.MustParse(demoTaskImageID), uuid.MustParse(demoTaskAudioID)})
	if err != nil {
		log.Fatal(err)
	}
	publishedAt := time.Date(2026, time.August, 1, 18, 30, 0, 0, time.UTC)
	_, err = tx.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,prompt,prompt_visibility,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Signal Architecture',
		       'A cinematic study of an angular cultural space shaped by warm directional light.',
		       'Cinematic architectural photography, angular contemporary gallery, warm coral light crossing cool concrete, human scale, subtle film grain, vertical composition',
		       'public','Demo reference media','published',
		       'Demo media for local development. No production model call or commercial license is implied.',$4)
		ON CONFLICT (id) DO UPDATE SET
			title=excluded.title,summary=excluded.summary,prompt=excluded.prompt,prompt_visibility=excluded.prompt_visibility,
			model_name=excluded.model_name,status='published',ai_disclosure=excluded.ai_disclosure,published_at=excluded.published_at,updated_at=now()`,
		uuid.MustParse(demoWorkID), uuid.MustParse(demoCreatorID), uuid.MustParse(demoAssetID), publishedAt)
	if err != nil {
		log.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO posts(id,author_id,work_id,body,status,published_at)
		VALUES($1,$2,$3,'Exploring how a single warm light path can organize an otherwise restrained architectural frame.','published',$4)
		ON CONFLICT (id) DO UPDATE SET body=excluded.body,status='published',published_at=excluded.published_at,updated_at=now()`,
		uuid.MustParse(demoPostID), uuid.MustParse(demoCreatorID), uuid.MustParse(demoWorkID), publishedAt)
	if err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("deterministic demo users, products, tasks, assets, work, and community content seeded")
}
