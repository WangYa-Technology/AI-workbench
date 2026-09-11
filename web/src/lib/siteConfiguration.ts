import type { SiteConfiguration } from '../api/client'

export const sitePolicyKeys = ['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'] as const

export function cloneSiteConfiguration(configuration: SiteConfiguration): SiteConfiguration {
  return {
    siteName: configuration.siteName,
    serverUrl: configuration.serverUrl,
    siteIconUrl: configuration.siteIconUrl,
    footerText: { ...configuration.footerText },
    policies: Object.fromEntries(sitePolicyKeys.map(key => [key, { ...configuration.policies[key] }])) as SiteConfiguration['policies'],
  }
}
