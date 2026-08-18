import type { CreationCapability } from '../api/client'

/**
 * Runtime JSON can be incomplete even when the HTTP request succeeded. Keep
 * the creation controls fail-closed until every contract field is present.
 */
export function isCreationCapabilityComplete(
  capability: CreationCapability | null | undefined,
): capability is CreationCapability {
  if (!capability || typeof capability.mode !== 'string' || typeof capability.available !== 'boolean' || typeof capability.supportsMask !== 'boolean') {
    return false
  }

  const arrays = [
    capability.aspectRatios,
    capability.qualities,
    capability.durationSeconds,
    capability.outputFormats,
    capability.resultFormats,
    capability.referenceKinds,
  ]
  if (!arrays.every(Array.isArray) || capability.outputFormats.length === 0 || capability.resultFormats.length === 0) {
    return false
  }

  if ((capability.mode === 'image' || capability.mode === 'video') && capability.aspectRatios.length === 0) {
    return false
  }
  if ((capability.mode === 'video' || capability.mode === 'music') && capability.durationSeconds.length === 0) {
    return false
  }
  return ['chat', 'image', 'video', 'music'].includes(capability.mode)
}
