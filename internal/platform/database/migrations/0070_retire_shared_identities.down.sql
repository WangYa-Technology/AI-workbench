DO $$ BEGIN
  RAISE EXCEPTION 'Shared identity erasure is irreversible; restoring shared credentials is not supported';
END $$;
