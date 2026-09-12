ALTER TABLE task_types ADD COLUMN scope text NOT NULL DEFAULT 'task' CHECK (scope IN ('task','community','marketplace'));
UPDATE task_types SET scope='task';
ALTER TABLE posts ADD COLUMN category text NOT NULL DEFAULT 'community_general';
ALTER TABLE products ADD COLUMN category text NOT NULL DEFAULT 'market_asset';
INSERT INTO task_types(code,name_zh,name_en,icon,sort_order,scope) VALUES
 ('community_general','综合','General','mixed',10,'community'), ('community_tutorial','教程','Tutorial','workflow',20,'community'),
 ('community_showcase','作品展示','Showcase','image',30,'community'),
 ('market_prompt','提示词','Prompt','prompt',10,'marketplace'), ('market_workflow','工作流','Workflow','workflow',20,'marketplace'),
 ('market_asset','素材','Asset','image',30,'marketplace'), ('market_work','作品授权','Work license','mixed',40,'marketplace');
UPDATE posts SET category='community_general';
UPDATE products SET category='market_' || product_type;
ALTER TABLE posts ADD CONSTRAINT posts_category_fk FOREIGN KEY (category) REFERENCES task_types(code);
ALTER TABLE products ADD CONSTRAINT products_category_fk FOREIGN KEY (category) REFERENCES task_types(code);
ALTER TABLE posts ALTER COLUMN category SET DEFAULT '';
ALTER TABLE products ALTER COLUMN category SET DEFAULT '';
CREATE FUNCTION validate_content_category() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected text := TG_ARGV[0];
BEGIN
 IF NEW.category = '' THEN
  SELECT code INTO NEW.category FROM task_types WHERE scope=expected ORDER BY sort_order,code LIMIT 1;
 END IF;
 IF NEW.category IS NULL OR NOT EXISTS (SELECT 1 FROM task_types WHERE code=NEW.category AND scope=expected FOR KEY SHARE) THEN
  RAISE EXCEPTION 'Invalid content category' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER posts_category_scope BEFORE INSERT OR UPDATE OF category ON posts FOR EACH ROW EXECUTE FUNCTION validate_content_category('community');
CREATE TRIGGER products_category_scope BEFORE INSERT OR UPDATE OF category ON products FOR EACH ROW EXECUTE FUNCTION validate_content_category('marketplace');
CREATE INDEX posts_category_feed_idx ON posts(category,published_at DESC,id DESC) WHERE status='published';
CREATE INDEX products_category_idx ON products(category);
CREATE FUNCTION validate_demand_category() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM task_types WHERE code=NEW.deliverable_type AND scope='task' FOR KEY SHARE) THEN
  RAISE EXCEPTION 'Invalid task category' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER demands_category_scope BEFORE INSERT OR UPDATE OF deliverable_type ON demands FOR EACH ROW EXECUTE FUNCTION validate_demand_category();
