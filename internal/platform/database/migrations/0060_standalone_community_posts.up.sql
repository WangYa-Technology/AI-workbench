ALTER TABLE posts ADD COLUMN title text;

UPDATE posts p
SET title = w.title
FROM works w
WHERE w.id = p.work_id AND p.title IS NULL;

ALTER TABLE posts
  ADD CONSTRAINT posts_title_length_check
  CHECK (work_id IS NOT NULL OR (title IS NOT NULL AND char_length(title) BETWEEN 3 AND 120));
