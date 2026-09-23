LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE asset_upload_commands,asset_upload_attempts IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before upload command rollback';
 END IF;
 IF EXISTS(SELECT 1 FROM asset_upload_commands) OR EXISTS(SELECT 1 FROM asset_upload_attempts) THEN
  RAISE EXCEPTION 'upload command evidence exists; rollback refused';
 END IF;
END $$;
DROP TRIGGER upload_command_result_guard ON assets;
DROP FUNCTION check_upload_command_result();
DROP TABLE asset_upload_attempts;
DROP FUNCTION protect_upload_attempt();
DROP TABLE asset_upload_commands;
DROP FUNCTION protect_upload_command();
DROP TRIGGER upload_command_worker_protocol ON jobs;
DROP FUNCTION check_upload_command_worker_protocol();
DROP TRIGGER upload_command_writer_protocol ON assets;
DROP FUNCTION check_upload_command_protocol();
DROP FUNCTION require_upload_command_protocol();
