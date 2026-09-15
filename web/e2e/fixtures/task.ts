import { execFileSync } from 'node:child_process'

/** Only prepares state in the disposable schema created by scripts/e2e.sh. */
export function assignTaskFixture(demandId: string, creatorId: string) {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
  if (!uuid.test(demandId) || !uuid.test(creatorId)) throw new Error('Invalid task fixture IDs')
  const url = process.env.TEST_DATABASE_URL || 'postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable'
  execFileSync('psql', [url, '--no-psqlrc', '--set=ON_ERROR_STOP=1', '--quiet'], {
    input: `BEGIN;
      SET LOCAL search_path TO hcai_e2e, public;
      DO $$ BEGIN
        IF to_regclass('hcai_e2e.demands') IS NULL THEN RAISE EXCEPTION 'Missing isolated E2E schema'; END IF;
      END $$;
      UPDATE hcai_e2e.demands SET status='assigned', assignee_id='${creatorId}', accepted_at=now() WHERE id='${demandId}' AND status='open';
      COMMIT;`,
    stdio: ['pipe', 'pipe', 'pipe'],
  })
}
