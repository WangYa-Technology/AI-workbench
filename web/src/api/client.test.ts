import { afterEach, describe, expect, it } from 'vitest'
import { i18n } from '../i18n'
import { APIError, messageFrom } from './client'

describe('localized API errors', () => {
  afterEach(() => {
    i18n.global.locale.value = 'en-US'
  })

  it('uses the current locale and retains the request support reference', () => {
    i18n.global.locale.value = 'zh-CN'
    const message = messageFrom(new APIError(401, {
      code: 'invalid_credentials',
      message: 'The email or password is incorrect.',
      retryable: false,
      requestId: 'req-localized-1',
    }))
    expect(message).toBe('邮箱或密码不正确。 支持参考编号：req-localized-1')
  })

  it('fails closed to a localized generic or retryable message', () => {
    i18n.global.locale.value = 'zh-CN'
    expect(messageFrom(new APIError(409, { code: 'future_code', message: 'English detail', retryable: false }))).toBe('请求未能完成，请检查输入后重试。')
    expect(messageFrom(new APIError(503, { code: 'future_code', message: 'English detail', retryable: true }))).toBe('服务暂时不可用，请稍后重试。')
  })
})
