import { i18n } from '../i18n'

export function creationPath(kind: string) {
  return `/create/${({ image: 'image', video: 'video', audio: 'music', music: 'music', document: 'chat', prompt: 'chat', workflow: 'chat', mixed: 'chat' } as Record<string, string>)[kind] || 'chat'}`
}

export function licenseLabel(code: string) {
  const key = code.startsWith('demo') ? 'demo' : code === 'creator-owned-local-test' ? 'owned' : ({ 'hcai-personal-v1': 'personal', 'hcai-commercial-v1': 'commercial' } as Record<string, string>)[code] || 'review'
  return i18n.global.t(`content.licenses.${key}`)
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
