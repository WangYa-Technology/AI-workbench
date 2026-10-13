#!/usr/bin/env python3
"""Local release evidence only. Dedicated loopback DB; no deployment or external providers.
Retains the three newly owned databases, binaries, backups and media for review.
"""
import concurrent.futures
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import urllib.parse
import urllib.request
import uuid
import shutil

ROOT = Path(__file__).resolve().parents[1]


def main():
    raw = os.environ.get('REHEARSAL_DATABASE_ADMIN_URL', '')
    url = urllib.parse.urlsplit(raw)
    if url.scheme not in ('postgres', 'postgresql') or url.hostname != '127.0.0.1' or url.path not in ('/hcai_goal5', '/hcai_e2e_admin') or url.query not in ('', 'sslmode=disable'):
        sys.exit('Explicit dedicated 127.0.0.1 hcai_e2e_admin/hcai_goal5 admin URL required; no production/default target is allowed.')
    os.umask(0o077)
    run = 'hcai_release_' + uuid.uuid4().hex
    out = ROOT / 'data/release-rehearsals' / run
    out.mkdir(parents=True)
    (out / 'bin').mkdir()
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'TMPDIR', 'GOCACHE', 'GOMODCACHE')}
    env.update(APP_ENV='test', LOCAL_PROVIDER_ENABLED='true', EMAIL_DELIVERY_MODE='disabled', STRIPE_ENABLED='false', WAFFO_ENABLED='false',
               MEDIA_ROOT=str(out / 'media'), LOCAL_PROVIDER_SOURCE=str(ROOT / 'web/public/media/home-cinematic.jpg'), PGTZ='UTC')
    children, handles = [], []
    evidence = {'run': run, 'externalCalls': 0, 'deployment': False, 'realAlertReceipt': False, 'steps': {}}
    def save():
        (out / 'result.json').write_text(json.dumps(evidence, indent=2) + '\n')
    def db(name):
        return urllib.parse.urlunsplit(url._replace(path='/' + run + '_' + name))
    def sql(target, query, schema='public'):
        return subprocess.check_output(['psql', target, '-X', '-qAt', '-v', 'ON_ERROR_STOP=1'], input=query, text=True,
                                       env={**env, 'PGOPTIONS': f'-c search_path={schema}'}, stderr=subprocess.PIPE).strip()
    def command(name, args, extra=None, expected=0):
        start = time.monotonic()
        with (out / (name + '.log')).open('w') as log:
            p = subprocess.Popen(args, cwd=ROOT, env={**env, **(extra or {})}, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            children.append(p)
            code = p.wait()
            children.remove(p)
        evidence['steps'][name] = {'exitCode': code, 'expectedExitCode': expected, 'seconds': round(time.monotonic()-start, 3)}
        save()
        if code != expected:
            raise RuntimeError(f'{name} failed: inspect retained log')
    def launch(name, args, extra=None):
        log = (out / (name + '.log')).open('w'); handles.append(log)
        p = subprocess.Popen(args, cwd=ROOT, env={**env, **(extra or {})}, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        children.append(p)
        return p
    def stop(p):
        if p.poll() is None:
            os.killpg(p.pid, signal.SIGTERM)
        try:
            p.wait(timeout=40)
        except subprocess.TimeoutExpired:
            os.killpg(p.pid, signal.SIGKILL); p.wait(timeout=5)
            raise RuntimeError('Graceful shutdown exceeded 40s')
        children.remove(p)
    def wait_for(check, timeout=60):
        deadline = time.monotonic()+timeout
        while time.monotonic() < deadline:
            if any(p.poll() is not None for p in children):
                raise RuntimeError('Owned service exited')
            try:
                if check(): return
            except (OSError, ValueError): pass
            time.sleep(.2)
        raise RuntimeError('Bounded condition timed out')
    def interrupt(signum, frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupt); signal.signal(signal.SIGINT, interrupt)
    files = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT).decode().split('\0')
    manifest = {p: hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in sorted(set(files)) if p and (ROOT/p).is_file()}
    (out/'source-sha256.json').write_text(json.dumps(manifest, indent=2)+'\n')
    evidence['head'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    evidence['dirty'] = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT))
    evidence['databases'] = [run+'_'+x for x in ('app','upgrade','restore')]
    migrations = sorted((ROOT/'internal/platform/database/migrations').glob('*.up.sql'))
    expected = [p.name for p in migrations]
    try:
        for name in ('app','upgrade','restore'):
            sql(raw, f'CREATE DATABASE {run}_{name}')
        for name in ('api','worker','migrate','configcheck'):
            command('build-'+name, ['go','build','-trimpath','-o',str(out/'bin'/name),'./cmd/'+name])
        command('build-seed', ['go','build','-trimpath','-o',str(out/'bin/seed'),'./internal/testfixtures/cmd/seed'])
        evidence['binarySHA256'] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (out/'bin').iterdir()}
        env['DATABASE_URL'] = db('app')
        command('migrate-empty', [str(out/'bin/migrate')])
        assert sql(db('app'),'SELECT version FROM schema_migrations ORDER BY version').splitlines() == expected
        # Repository-verifiable baseline, NOT an assertion about the deployed production release.
        baseline = 'ad93fd9'
        paths = subprocess.check_output(['git','ls-tree','-r','--name-only',baseline,'internal/platform/database/migrations'],cwd=ROOT,text=True).splitlines()
        old = sorted(p for p in paths if p.endswith('.up.sql'))
        assert len(old) == 177
        bodies = ['CREATE TABLE schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());']
        old_hashes = {}
        for p in old:
            body = subprocess.check_output(['git','show',f'{baseline}:{p}'],cwd=ROOT,text=True)
            old_hashes[Path(p).name] = hashlib.sha256(body.encode()).hexdigest()
            bodies += ['BEGIN;', body, f"INSERT INTO schema_migrations(version) VALUES('{Path(p).name}'); COMMIT;"]
        sql(db('upgrade'), '\n'.join(bodies))
        (out/'baseline-migrations.json').write_text(json.dumps(old_hashes,indent=2)+'\n')
        command('migrate-previous', [str(out/'bin/migrate')], {'DATABASE_URL':db('upgrade')})
        assert sql(db('upgrade'),'SELECT version FROM schema_migrations ORDER BY version').splitlines() == expected
        evidence['migrationCount'] = len(expected)
        sql(db('app'), 'CREATE SCHEMA release_fixture')
        env['DATABASE_URL'] = db('app') + ('&' if url.query else '?')+'search_path=release_fixture'
        command('seed-isolated', [str(out/'bin/seed')])
        with socket.socket() as s:
            s.bind(('127.0.0.1',0)); port=s.getsockname()[1]
        origin=f'http://127.0.0.1:{port}'
        env.update(HTTP_ADDR=f'127.0.0.1:{port}',WEB_ORIGIN=origin)
        jar=http.cookiejar.CookieJar()
        client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.ProxyHandler({}))
        def request(path, data=None, key=None):
            headers={'Content-Type':'application/json'}
            if key: headers['Idempotency-Key']=key
            req=urllib.request.Request(origin+path,data=json.dumps(data).encode() if data is not None else None,headers=headers)
            with client.open(req,timeout=10) as r: return r.read()
        api=launch('api', [str(out/'bin/api')])
        wait_for(lambda: bool(request('/ready')))
        meta=json.loads(request('/api/v1/meta'))
        assert meta['paymentProvider']['enabled'] is False
        workers=[launch('worker-before-'+str(i),[str(out/'bin/worker')]) for i in range(2)]
        wait_for(lambda: all('worker started' in (out/f'worker-before-{i}.log').read_text() for i in range(2)))
        for w in workers:stop(w)
        request('/api/v1/auth/login',{'email':'creator@fixture.hcai.test','password':'fixture-password-2026'})
        key='release-'+uuid.uuid4().hex
        body={'mode':'image','prompt':'Isolated release restart and restore evidence'}
        generation=json.loads(request('/api/v1/generations',body,key)); gid=str(uuid.UUID(generation['id']))
        state=f"SELECT g.status||'|'||r.status||'|'||(SELECT count(*) FROM assets WHERE source_type='generation' AND source_id=g.id)||'|'||(SELECT count(*) FROM point_entries WHERE operation_id=g.id AND entry_type='generation_charge') FROM generations g JOIN point_reservations r ON r.generation_id=g.id WHERE g.id='{gid}'"
        assert sql(db('app'),state,'release_fixture') == 'queued|held|0|0'
        workers=[launch('worker-after-'+str(i),[str(out/'bin/worker')]) for i in range(2)]
        wait_for(lambda: sql(db('app'),state,'release_fixture')=='succeeded|captured|1|1')
        assert json.loads(request('/api/v1/generations',body,key))['id']==gid
        asset=json.loads(request('/api/v1/generations/'+gid))['outputAssetId']
        content=request('/api/v1/assets/'+asset+'/content')
        evidence['restart']={'generationId':gid,'before':'queued|held|0|0','after':sql(db('app'),state,'release_fixture'),'sameKeySameId':True,'workers':2}
        # A small read-only diagnostic workload, not a production capacity certification.
        def sample(_):
            start=time.monotonic()
            with urllib.request.urlopen(origin+'/ready',timeout=5) as r:assert r.status==200
            return (time.monotonic()-start)*1000
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool: latency=sorted(pool.map(sample,range(100)))
        evidence['readinessLoad']={'requests':100,'concurrency':4,'p50Ms':latency[49],'p95Ms':latency[94],'maxMs':max(latency)}
        wait_for(lambda: sql(db('app'),"SELECT count(*) FROM jobs WHERE status IN ('queued','running') AND available_at<=now()",'release_fixture')=='0')
        command('metrics-healthy',['bash','scripts/metrics-alert-check.sh'],{'METRICS_URL':origin+'/metrics','ALERT_PAYMENT_MODE':'all'})
        (out/'metrics.txt').write_bytes(request('/metrics'))
        for w in workers:stop(w)
        stop(api)
        command('metrics-api-down',['bash','scripts/metrics-alert-check.sh'],{'METRICS_URL':origin+'/metrics'},expected=1)
        def data_snapshot(target):
            tables=sql(target,"SELECT quote_ident(schemaname)||'.'||quote_ident(tablename) FROM pg_tables WHERE schemaname IN ('public','release_fixture') ORDER BY 1").splitlines()
            return {table:sql(target,f"SELECT count(*)||':'||md5(coalesce(string_agg(md5(to_jsonb(t)::text),'' ORDER BY md5(to_jsonb(t)::text)),'')) FROM {table} t") for table in tables}
        source=data_snapshot(db('app'))
        command('backup',['bash','scripts/database-backup.sh'],{'BACKUP_DATABASE_URL':db('app'),'BACKUP_DIR':str(out/'backups')})
        archive=next((out/'backups').glob('*.dump')); assert Path(str(archive)+'.sha256').exists()
        command('restore',['bash','scripts/database-restore.sh',str(archive)],{'RESTORE_DATABASE_URL':db('restore'),'RESTORE_CONFIRM':'I_UNDERSTAND_DATA_WILL_BE_REPLACED'})
        restored=data_snapshot(db('restore'));assert source==restored
        (out/'table-digests.json').write_text(json.dumps(source,indent=2)+'\n')
        assert sql(db('restore'),"SELECT count(*) FROM pg_index WHERE NOT indisvalid")=='0'
        shutil.copytree(out/'media',out/'media-backup'); shutil.copytree(out/'media-backup',out/'media-restored')
        def media_hashes(root):return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file()}
        assert media_hashes(out/'media')==media_hashes(out/'media-restored')
        evidence['restore']={'equalTables':len(source),'equalMediaFiles':len(media_hashes(out/'media')),'archiveSHA256':hashlib.sha256(archive.read_bytes()).hexdigest()}
        env.update(DATABASE_URL=db('restore')+('&' if url.query else '?')+'search_path=release_fixture',MEDIA_ROOT=str(out/'media-restored'))
        command('migrate-restored',[str(out/'bin/migrate')])
        api=launch('api-restored',[str(out/'bin/api')]);wait_for(lambda:bool(request('/ready')))
        request('/api/v1/auth/login',{'email':'creator@fixture.hcai.test','password':'fixture-password-2026'})
        assert request('/api/v1/assets/'+asset+'/content')==content
        assert json.loads(request('/api/v1/generations',body,key))['id']==gid
        stop(api)
        evidence['restore']['httpContentMatches']=True
        evidence['status']='passed_isolated_only'
    except BaseException:
        evidence['status']='failed'
        raise
    finally:
        for p in list(reversed(children)):
            try: stop(p)
            except (OSError, RuntimeError): evidence['shutdownFailure']=True
        evidence['processesStopped']=not children
        for h in handles:h.close()
        save()
        print('Release rehearsal evidence: '+str(out),flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
