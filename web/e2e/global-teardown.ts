import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export default function globalTeardown() {
  const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
  execFileSync(path.join(projectRoot, 'scripts/e2e-cleanup.sh'), { cwd: projectRoot, env: process.env, stdio: 'inherit' })
}
