export class DownloadCheckError extends Error {
  constructor(readonly reason: 'accessChanged' | 'fileMissing' | 'downloadUnavailable') { super(reason) }
}

// Inspect a bounded range; never materialize a purchased package as a Blob.
// The subsequent native browser download must still pass server authorization.
export async function checkLicensedDownload(url: string, signal: globalThis.AbortSignal) {
  const response = await globalThis.fetch(url, { credentials: 'include', cache: 'no-store', signal, headers: { Range: 'bytes=0-0' } })
  try {
    if (response.status === 401 || response.status === 403) throw new DownloadCheckError('accessChanged')
    if (response.status === 404) throw new DownloadCheckError('fileMissing')
    if (!response.ok) throw new DownloadCheckError('downloadUnavailable')
  } finally {
    await response.body?.cancel()
  }
}
