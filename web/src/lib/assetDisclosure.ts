import type { Asset } from '../api/client'
import { i18n } from '../i18n'

// Provenance identifies a generator, not whether a call incurred a charge.
// Uploaded/legacy assets need an explicit statement from the publisher.
export function assetDisclosure(asset: Asset | null) {
  const generation = asset?.provenance?.generation
  if (!generation?.provider || !generation.modelName || asset?.sourceType !== 'generation') return ''
  return i18n.global.t('publish.evidenceDisclosure', { provider: generation.provider, model: generation.modelName })
}
