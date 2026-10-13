-- Run after applying migrations and before enabling task delivery in an
-- environment. This is read-only and intentionally returns no secrets.
SELECT 'migration_0180' AS check_name,
       CASE WHEN EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='0180_task_delivery_kind.up.sql')
            THEN 'pass' ELSE 'fail' END AS result;

SELECT 'migration_0181' AS check_name,
       CASE WHEN EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='0181_task_delivery_asset_indexes.up.sql')
            THEN 'pass' ELSE 'fail' END AS result;

SELECT 'task_type_delivery_kind_complete' AS check_name,
       CASE WHEN NOT EXISTS (
         SELECT 1 FROM task_types
         WHERE delivery_kind IS NULL
            OR delivery_kind NOT IN ('image','video','audio','document','prompt','workflow','mixed')
       ) THEN 'pass' ELSE 'fail' END AS result;

SELECT 'task_delivery_kind_complete' AS check_name,
       CASE WHEN NOT EXISTS (
         SELECT 1 FROM demands
         WHERE delivery_kind IS NULL
            OR delivery_kind NOT IN ('image','video','audio','document','prompt','workflow','mixed')
       ) THEN 'pass' ELSE 'fail' END AS result;

SELECT 'task_delivery_kind_mismatch_count' AS check_name,
       count(*)::text AS result
FROM demands d
JOIN task_types t ON t.code=d.deliverable_type
WHERE d.delivery_kind IS DISTINCT FROM t.delivery_kind;
