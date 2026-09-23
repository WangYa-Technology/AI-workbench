// These credentials exist only in the isolated schema populated by internal/testfixtures/cmd/seed.
export function fixtureCredentials(actor: 'creator' | 'publisher' | 'admin' | 'studio') {
  const names = { creator: 'creator', publisher: 'publisher', admin: 'operations', studio: 'studio' }
  return { email: `${names[actor]}@fixture.hcai.test`, password: 'fixture-password-2026' }
}
