export function openCheckoutWindow(url = 'about:blank'): Window | null {
  const checkoutWindow = globalThis.open('about:blank', '_blank')
  if (!checkoutWindow) return null
  checkoutWindow.opener = null
  if (url !== 'about:blank') checkoutWindow.location.replace(url)
  return checkoutWindow
}
