import { afterEach, expect, test, vi } from 'vitest'
import { openCheckoutWindow } from './checkout'

afterEach(() => vi.unstubAllGlobals())

test('returns null when the browser blocks the checkout window', () => {
  vi.stubGlobal('open', vi.fn(() => null))
  expect(openCheckoutWindow()).toBeNull()
})

test('severs the opener before navigating to hosted checkout', () => {
  const replace = vi.fn()
  const checkoutWindow = { opener: {}, location: { replace } } as unknown as Window
  const open = vi.fn(() => checkoutWindow)
  vi.stubGlobal('open', open)

  expect(openCheckoutWindow('https://checkout.example.test/session')).toBe(checkoutWindow)
  expect(open).toHaveBeenCalledWith('about:blank', '_blank')
  expect(checkoutWindow.opener).toBeNull()
  expect(replace).toHaveBeenCalledWith('https://checkout.example.test/session')
})
