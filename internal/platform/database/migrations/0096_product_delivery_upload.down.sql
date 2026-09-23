DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_delivery_repairs WHERE source_kind='upload') THEN
  RAISE EXCEPTION 'cannot remove uploaded delivery repair evidence';
 END IF;
END $$;
ALTER TABLE product_delivery_repairs
 DROP CONSTRAINT product_delivery_repair_source_kind,
 ALTER COLUMN source_backend SET NOT NULL,
 ALTER COLUMN source_key SET NOT NULL,
 DROP COLUMN source_kind;
