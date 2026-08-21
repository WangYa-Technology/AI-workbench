INSERT INTO permissions(id,module,description,risk_level,resource_authorization)
VALUES ('admin:audit','admin','Read immutable audit evidence','medium',false)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('admin','admin:audit'),('moderator','admin:audit')
ON CONFLICT DO NOTHING;
