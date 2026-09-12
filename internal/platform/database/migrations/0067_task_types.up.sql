CREATE TABLE task_types (
  code text PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_-]{0,47}$'),
  name_zh text NOT NULL CHECK (char_length(name_zh) BETWEEN 1 AND 80),
  name_en text NOT NULL CHECK (char_length(name_en) BETWEEN 1 AND 80),
  icon text NOT NULL CHECK (icon IN ('image','video','audio','prompt','workflow','mixed')),
  sort_order integer NOT NULL DEFAULT 0,
  version integer NOT NULL DEFAULT 1
);
INSERT INTO task_types(code,name_zh,name_en,icon,sort_order) VALUES
 ('image','图片','Image','image',10), ('video','视频','Video','video',20),
 ('audio','音频','Audio','audio',30), ('prompt','提示词','Prompt','prompt',40),
 ('workflow','工作流','Workflow','workflow',50), ('mixed','混合','Mixed','mixed',60);
ALTER TABLE demands DROP CONSTRAINT demands_deliverable_type_check;
ALTER TABLE demands ADD CONSTRAINT demands_task_type_fk FOREIGN KEY (deliverable_type) REFERENCES task_types(code);
