import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test, type Page } from '@playwright/test'
import { gotoAndEnsureAuth, waitForGraphQLOperation } from './auth.utils'

const managementSecret = 'e2e-management-secret'

// Retries reuse the backend database, so every test starts from a clean instance
// list. Only leftovers created by this suite are removed: unrelated instances must
// survive a run against a shared backend.
async function deleteAllCPAInstances(page: Page) {
  const ids = await page.evaluate(async () => {
    const token = localStorage.getItem('axonhub_access_token')
    const response = await fetch('/admin/graphql', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      body: JSON.stringify({ query: 'query { cpaInstances { id name } }' }),
    })
    const payload = (await response.json()) as { data?: { cpaInstances?: { id: number; name: string }[] } }
    return (payload.data?.cpaInstances ?? [])
      .filter((instance) => /^CPA (E2E|reset) /.test(instance.name))
      .map((instance) => instance.id)
  })
  for (const id of ids) {
    await page.evaluate(async (instanceID) => {
      const token = localStorage.getItem('axonhub_access_token')
      await fetch('/admin/graphql', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({
          query: 'mutation DeleteCPAInstance($id: Int!) { deleteCPAInstance(id: $id) }',
          variables: { id: instanceID },
        }),
      })
    }, id)
  }
  if (ids.length > 0) await page.reload({ waitUntil: 'domcontentloaded' })
}

// Dev-mode routing occasionally lands on sign-in first; retry once before failing.
async function openCPA(page: Page) {
  await gotoAndEnsureAuth(page, '/cpa')
  const title = page.getByTestId('cpa-page-title')
  try {
    await title.waitFor({ state: 'visible', timeout: 15_000 })
  } catch {
    await gotoAndEnsureAuth(page, '/cpa')
    await title.waitFor({ state: 'visible', timeout: 15_000 })
  }
}

type MockCredential = {
  auth_index: string
  name: string
  type: string
  email: string
  status: string
  disabled: boolean
  account_type: string
  id_token?: { chatgpt_account_id: string }
  priority: number
}

test.describe('CPA critical paths', () => {
  test.describe.configure({ mode: 'serial' })
  let server: Server
  let baseURL: string
  let failPatch = false
  let providerCalls = 0
  let creditIDs = ['late', 'soon', 'middle']
  let consumedCredits: string[] = []
  // Claims are global per account + credit, so retries need a fresh account id.
  let codexAccountID = 'e2e-codex-account'
  // Counts how many reset-credit list calls the mock should fail to emulate a flaky tunnel.
  let resetListFailures = 0
  const credentials: MockCredential[] = Array.from({ length: 55 }, (_, index) => ({
    auth_index: `e2e-auth-${index + 1}`,
    name: `e2e-codex-${index + 1}.json`,
    type: 'codex',
    email: `cpa-e2e-${String(index + 1).padStart(2, '0')}@example.com`,
    status: index % 10 === 0 ? 'disabled' : 'active',
    disabled: index % 10 === 0,
    account_type: index % 2 === 0 ? 'plus' : 'team',
    priority: index % 4,
  }))

  test.beforeAll(async () => {
    server = createServer((request, response) => {
      if (request.headers.authorization !== `Bearer ${managementSecret}`) {
        response.writeHead(401).end('{}')
        return
      }

      const chunks: Buffer[] = []
      request.on('data', (chunk) => chunks.push(Buffer.from(chunk)))
      request.on('end', () => {
        response.setHeader('Content-Type', 'application/json')
        response.setHeader('X-CPA-VERSION', '7.2.0')
        const body = chunks.length > 0 ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : undefined

        if (request.method === 'GET' && request.url === '/v0/management/auth-files') {
          response.end(JSON.stringify(credentials))
          return
        }
        if (request.method === 'POST' && request.url === '/v0/management/api-call') {
          providerCalls += 1
          let providerBody: unknown = {
            plan_type: 'plus',
            rate_limit: { primary_window: { used_percent: 25, limit_window_seconds: 18_000, reset_after_seconds: 600 } },
          }
          if (body?.url === 'https://chatgpt.com/backend-api/wham/rate-limit-reset-credits') {
            if (resetListFailures > 0) {
              resetListFailures -= 1
              response.end(JSON.stringify({ status_code: 502, header: { 'Content-Type': ['application/json'] }, body: '{}' }))
              return
            }
            providerBody = {
              available_count: creditIDs.length,
              credits: [
                { id: 'late', title: 'Late', reset_type: 'weekly', status: 'available', expires_at: new Date(Date.now() + 10 * 24 * 60 * 60_000).toISOString() },
                { id: 'soon', title: 'Soon', reset_type: 'weekly', status: 'available', expires_at: new Date(Date.now() + 2 * 24 * 60 * 60_000).toISOString() },
                { id: 'middle', title: 'Middle', reset_type: 'weekly', status: 'available', expires_at: new Date(Date.now() + 5 * 24 * 60 * 60_000).toISOString() },
              ].filter((credit) => creditIDs.includes(credit.id)),
            }
          } else if (body?.url === 'https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume') {
            const request = JSON.parse(body.data) as { credit_id: string; redeem_request_id: string }
            if (body.auth_index !== credentials[0].auth_index || body.header?.Authorization !== 'Bearer $TOKEN$' || body.header?.['Chatgpt-Account-Id'] !== codexAccountID || !request.redeem_request_id) {
              response.writeHead(400).end('{}')
              return
            }
            consumedCredits.push(request.credit_id)
            creditIDs = creditIDs.filter((id) => id !== request.credit_id)
            providerBody = { code: 'reset', credit: { id: request.credit_id, status: 'redeemed' } }
          }
          response.end(JSON.stringify({ status_code: 200, header: { 'Content-Type': ['application/json'] }, body: JSON.stringify(providerBody) }))
          return
        }
        if (request.method === 'PATCH' && request.url === '/v0/management/auth-files/status') {
          if (failPatch) {
            response.writeHead(503).end('{}')
            return
          }
          const credential = credentials.find(
            (item) => item.name === body?.name && (!body?.auth_index || item.auth_index === body.auth_index)
          )
          if (!credential) {
            response.writeHead(404).end('{}')
            return
          }
          credential.disabled = Boolean(body.disabled)
          credential.status = credential.disabled ? 'disabled' : 'active'
          response.end(JSON.stringify({ status: 'ok' }))
          return
        }
        response.writeHead(404).end('{}')
      })
    })
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject)
      server.listen(0, '127.0.0.1', resolve)
    })
    const address = server.address() as AddressInfo
    baseURL = `http://127.0.0.1:${address.port}`
  })

  test.afterAll(async () => {
    await new Promise<void>((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())))
  })

  test('creates, queries, refreshes, toggles, edits, and deletes an instance', async ({ page }) => {
    test.setTimeout(90_000)
    const suffix = Date.now().toString().slice(-6)
    const instanceName = `CPA E2E ${suffix}`

    await openCPA(page)
    await deleteAllCPAInstances(page)
    await expect(page.getByTestId('cpa-page-title')).toBeVisible({ timeout: 30_000 })
    await page.getByTestId('cpa-add-instance').click()

    const dialog = page.getByTestId('cpa-instance-dialog')
    await dialog.getByTestId('cpa-instance-name').fill(instanceName)
    await dialog.getByTestId('cpa-instance-base-url').fill(baseURL)
    await dialog.getByTestId('cpa-instance-secret').fill(managementSecret)
    await dialog.getByTestId('cpa-insecure-tls').click()
    await dialog.getByTestId('cpa-insecure-confirm').click()
    await Promise.all([
      waitForGraphQLOperation(page, 'CreateCPAInstance'),
      dialog.getByTestId('cpa-instance-submit').click(),
    ])
    await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('cpa-credential-table')).toBeVisible()
    await expect(page.getByText(/55/).first()).toBeVisible()
    await expect(page.getByTestId('cpa-provider-codex')).toBeVisible()

    await expect(page.getByTestId('cpa-pagination-next')).toBeEnabled()
    await page.getByTestId('cpa-pagination-next').click()
    await expect(page.locator('[data-testid^="cpa-credential-row-"]')).toHaveCount(5)
    await page.getByTestId('cpa-pagination-previous').click()
    await expect(page.locator('[data-testid^="cpa-credential-row-"]')).toHaveCount(50)

    await page.getByTestId('cpa-provider-codex').click()
    const planFilter = page.getByTestId('cpa-plan-filter').getByRole('button')
    await expect(planFilter).toBeVisible()
    await planFilter.click()
    const plusPlan = page
      .getByRole('option', { name: /Plus|plus/i })
      .or(page.locator('[role="option"]').filter({ hasText: /Plus|plus/i }))
    await expect(plusPlan.first()).toBeVisible()
    await plusPlan.first().click()
    await expect(page.locator('[data-testid^="cpa-credential-row-"]')).not.toHaveCount(50)

    await page.getByTestId('cpa-provider-codex').click()
    await expect(page.getByTestId('cpa-status-filter')).toBeVisible()
    const statusFilter = page.getByTestId('cpa-status-filter').getByRole('button')
    await statusFilter.click()
    const disabledOption = page
      .getByRole('option', { name: /Disabled|已禁用/i })
      .or(page.locator('[role="option"]').filter({ hasText: /Disabled|已禁用/i }))
    await expect(disabledOption.first()).toBeVisible()
    await disabledOption.first().click()
    await expect(page.locator('[data-testid^="cpa-credential-row-"]')).not.toHaveCount(50)
    await disabledOption.first().click()
    await page.getByTestId('cpa-provider-all').click()

    await page.getByTestId('cpa-search').fill('cpa-e2e-55@example.com')
    const searchedRow = page.locator('[data-testid^="cpa-credential-row-"]').filter({ hasText: 'cpa-e2e-55@example.com' })
    await expect(searchedRow).toBeVisible({ timeout: 10_000 })
    await page.getByTestId('cpa-search').fill('')

    const firstRow = page.locator('[data-testid^="cpa-credential-row-"]').first()
    await expect(firstRow).toBeVisible()
    const rowTestID = await firstRow.getAttribute('data-testid')
    const credentialID = rowTestID?.replace('cpa-credential-row-', '')
    expect(credentialID).toBeTruthy()

    const callsBeforeRefresh = providerCalls
    await Promise.all([
      waitForGraphQLOperation(page, 'RefreshCPACredential'),
      page.getByTestId(`cpa-refresh-credential-${credentialID}`).click(),
    ])
    // The instance scheduler may refresh every credential in the background, so
    // only the lower bound is deterministic.
    await expect.poll(() => providerCalls).toBeGreaterThanOrEqual(callsBeforeRefresh + 1)

    const wasDisabled = await firstRow.getByText(/Disabled|已禁用/i).isVisible().catch(() => false)
    await page.getByTestId(`cpa-toggle-credential-${credentialID}`).click()
    await Promise.all([
      waitForGraphQLOperation(page, 'ToggleCPACredential'),
      page.getByTestId('cpa-toggle-confirm').click(),
    ])
    if (wasDisabled) {
      await expect(firstRow.getByText(/Disabled|已禁用/i)).not.toBeVisible()
    } else {
      await expect(firstRow.getByText(/Disabled|已禁用/i)).toBeVisible()
    }

    failPatch = true
    await page.getByTestId(`cpa-toggle-credential-${credentialID}`).click()
    await Promise.all([
      waitForGraphQLOperation(page, 'ToggleCPACredential'),
      page.getByTestId('cpa-toggle-confirm').click(),
    ])
    if (wasDisabled) {
      await expect(firstRow.getByText(/Disabled|已禁用/i)).not.toBeVisible()
    } else {
      await expect(firstRow.getByText(/Disabled|已禁用/i)).toBeVisible()
    }
    // A failed toggle keeps the confirmation dialog open by design; dismiss it
    // before the dialog overlay starts blocking unrelated clicks.
    await page.getByRole('button', { name: /^(Cancel|取消)$/ }).click()
    await expect(page.getByTestId('cpa-toggle-confirm')).toHaveCount(0)
    failPatch = false

    await page.getByTestId('cpa-edit-instance').click()
    const editDialog = page.getByTestId('cpa-instance-dialog')
    await expect(editDialog.getByTestId('cpa-instance-secret')).toHaveValue('')
    await editDialog.getByTestId('cpa-instance-name').fill(`${instanceName} updated`)
    // Editing an insecure-TLS instance re-requires the certificate risk confirmation.
    const insecureConfirm = editDialog.getByTestId('cpa-insecure-confirm')
    if (await insecureConfirm.isVisible().catch(() => false)) await insecureConfirm.click()
    await Promise.all([
      waitForGraphQLOperation(page, 'UpdateCPAInstance'),
      editDialog.getByTestId('cpa-instance-submit').click(),
    ])
    await expect(editDialog).not.toBeVisible({ timeout: 15_000 })

    await page.getByTestId('cpa-delete-instance').click()
    await Promise.all([
      waitForGraphQLOperation(page, 'DeleteCPAInstance'),
      page.getByTestId('cpa-delete-confirm').click(),
    ])
    await expect(page.getByTestId('cpa-instance-select')).not.toContainText(`${instanceName} updated`)
  })

  test('confirms the selected Codex credit once and saves automatic reset', async ({ page }) => {
    test.setTimeout(90_000)
    creditIDs = ['late', 'soon', 'middle']
    consumedCredits = []
    resetListFailures = 0
    codexAccountID = `e2e-codex-account-${Date.now()}`
    credentials[0].id_token = { chatgpt_account_id: codexAccountID }
    const instanceName = `CPA reset ${Date.now()}`
    await openCPA(page)
    await deleteAllCPAInstances(page)
    await expect(page.getByTestId('cpa-page-title')).toBeVisible({ timeout: 30_000 })
    await page.getByTestId('cpa-add-instance').click()
    const dialog = page.getByTestId('cpa-instance-dialog')
    await dialog.getByTestId('cpa-instance-name').fill(instanceName)
    await dialog.getByTestId('cpa-instance-base-url').fill(baseURL)
    await dialog.getByTestId('cpa-instance-secret').fill(managementSecret)
    await expect(dialog.getByTestId('cpa-auto-reset')).toHaveAttribute('data-state', 'unchecked')
    await dialog.getByTestId('cpa-instance-submit').click()
    await expect(dialog).not.toBeVisible({ timeout: 15_000 })
    await page.getByTestId('cpa-search').fill('cpa-e2e-01@example.com')
    const row = page.locator('[data-testid^="cpa-credential-row-"]').filter({ hasText: 'cpa-e2e-01@example.com' })
    await expect(row).toBeVisible({ timeout: 10_000 })
    const credentialID = Number((await row.getAttribute('data-testid'))?.replace('cpa-credential-row-', ''))
    const cardRefresh = () => page.getByTestId(`cpa-refresh-credential-${credentialID}`).click()
    const credits = page.getByTestId(`cpa-reset-credits-${credentialID}`)
    // Reset cards arrive with the quota refresh, and one transient read failure
    // is absorbed by the generic provider-read retry.
    resetListFailures = 1
    await cardRefresh()
    await expect(page.getByTestId(`cpa-refresh-credential-${credentialID}`)).toBeEnabled({ timeout: 20_000 })
    await row.getByRole('button').first().click()
    await expect(credits).toContainText(/3 reset credits remaining|剩余 3 次/)
    // Three failures exhaust the retry budget: the refresh reports the card read
    // as failed instead of showing "no cards".
    resetListFailures = 3
    await cardRefresh()
    await expect(page.getByTestId(`cpa-reset-failed-${credentialID}`)).toBeVisible({ timeout: 20_000 })
    await expect(credits).toHaveCount(0)
    await cardRefresh()
    await expect(credits).toContainText(/3 reset credits remaining|剩余 3 次/, { timeout: 20_000 })
    await expect(credits).toContainText(/Soon/)
    await expect(credits).toContainText(/Late/)
    await expect(credits).toContainText(/Middle/)
    await expect(credits).toContainText(/Earliest expiration|最早过期/)
    await page.getByTestId(`cpa-reset-credential-${credentialID}`).click()
    await expect(page.getByTestId('cpa-reset-confirmation')).toContainText('cpa-e2e-01@example.com')
    await page.getByTestId('cpa-reset-confirmation').getByRole('button', { name: /Cancel|取消/ }).click()
    expect(consumedCredits).toEqual([])
    await page.getByTestId(`cpa-reset-credential-${credentialID}`).click()
    await page.getByTestId('cpa-reset-confirm').click()
    await expect(page.getByTestId('cpa-reset-confirmation')).not.toBeVisible()
    expect(consumedCredits).toEqual(['soon'])
    await expect(credits).toContainText(/2 reset credits remaining|剩余 2 次/)
    const replay = await page.evaluate(async (id) => {
      const token = localStorage.getItem('axonhub_access_token')
      const result = await fetch('/admin/graphql', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ query: 'mutation ResetCPACodexCredential($credentialID: Int!, $creditID: String!) { resetCPACodexCredential(credentialID: $credentialID, creditID: $creditID) }', variables: { credentialID: id, creditID: 'soon' } }),
      })
      return result.json()
    }, credentialID)
    expect(replay.errors?.length).toBeGreaterThan(0)
    expect(consumedCredits).toEqual(['soon'])
    creditIDs = []
    await cardRefresh()
    await expect(page.getByTestId(`cpa-reset-empty-${credentialID}`)).toBeVisible({ timeout: 20_000 })
    await expect(credits).toHaveCount(0)
    await expect(page.getByTestId(`cpa-reset-credential-${credentialID}`)).toHaveCount(0)
    await page.getByTestId('cpa-edit-instance').click()
    await expect(dialog.getByTestId('cpa-instance-name')).toHaveValue(instanceName)
    // Auto use depends on the patrol that refreshes quota and reset credits.
    await expect(dialog.getByTestId('cpa-auto-reset')).toBeDisabled()
    await dialog.getByTestId('cpa-auto-manage').click()
    await expect(dialog.getByTestId('cpa-auto-reset')).toBeEnabled()
    await dialog.getByTestId('cpa-auto-reset').click()
    await dialog.getByTestId('cpa-instance-submit').click()
    await expect(dialog).not.toBeVisible()
    await page.getByTestId('cpa-edit-instance').click()
    await expect(dialog.getByTestId('cpa-auto-reset')).toHaveAttribute('data-state', 'checked')
    // Turning the dependency off clears and disables auto use again.
    await dialog.getByTestId('cpa-auto-manage').click()
    await expect(dialog.getByTestId('cpa-auto-reset')).toBeDisabled()
    await expect(dialog.getByTestId('cpa-auto-reset')).toHaveAttribute('data-state', 'unchecked')
    await dialog.getByTestId('cpa-instance-submit').click()
    await expect(dialog).not.toBeVisible()
    await page.getByTestId('cpa-edit-instance').click()
    await expect(dialog.getByTestId('cpa-auto-reset')).toHaveAttribute('data-state', 'unchecked')
    await dialog.getByRole('button', { name: /Cancel|取消/ }).click()
    await page.getByTestId('cpa-delete-instance').click()
    await page.getByTestId('cpa-delete-confirm').click()
    await expect(page.getByTestId('cpa-instance-select')).not.toContainText(instanceName)
  })
})
