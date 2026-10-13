import { i18n } from '../i18n'

export function creationPath(kind: string) {
  return `/create/${({ image: 'image', video: 'video', audio: 'music', music: 'music', document: 'chat', prompt: 'chat', workflow: 'chat', mixed: 'chat' } as Record<string, string>)[kind] || 'chat'}`
}

export function licenseKind(code: string) {
  return ({ 'creator-owned': 'owned', 'creator-owned-local-test': 'owned',
    'hcai-personal-v1': 'personal', 'hcai-commercial-v1': 'commercial',
    'hcai-commercial-standard-v1': 'commercial', 'task-contract': 'task' } as Record<string, string>)[code] || 'review'
}
export function licenseLabel(code: string) {
  return i18n.global.t(`content.licenses.${licenseKind(code)}`)
}
export function licenseExplanation(code: string) {
  return i18n.global.t(`content.licenseScopes.${licenseKind(code)}`)
}

const listPaths = ['/discover', '/community', '/market', '/market/demands', '/workspace/assets', '/workspace/purchases']
export function rememberContentList(path: string, fullPath: string) {
  if (!listPaths.includes(path)) return
  try { sessionStorage.setItem(`content-list:${path === '/workspace/purchases' ? '/workspace/assets' : path}`, fullPath) } catch { /* Storage is optional. */ }
}
export function contentListReturn(path: string) {
  try {
    const saved = sessionStorage.getItem(`content-list:${path}`)
    const allowed = path === '/workspace/assets' ? [path, '/workspace/purchases'] : [path]
    if (saved && allowed.some(base => saved === base || saved.startsWith(`${base}?`))) return saved
  } catch { /* Fall back to the unfiltered list. */ }
  return path
}
