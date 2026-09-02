import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from '@playwright/test'
import { gotoAndEnsureAuth, waitForGraphQLOperation } from './auth.utils'

const managementSecret = 'e2e-management-secret'

type MockCredential = {
  auth_index: string
  name: string
  type: string
  email: string
  status: string
  disabled: boolean
  account_type: string
  priority: number
}

test.describe('CPA critical paths', () => {
  test.describe.configure({ mode: 'serial' })

  let server: Server
  let baseURL: string
  let failPatch = false
  let providerCalls = 0
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
          response.end(
            JSON.stringify({
              status_code: 200,
              header: { 'Content-Type': ['application/json'] },
              body: JSON.stringify({
                plan_type: 'plus',
                rate_limit: {
                  primary_window: {
                    used_percent: 25,
                    limit_window_seconds: 18_000,
                    reset_after_seconds: 600,
                  },
                },
              }),
            })
          )
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

    await gotoAndEnsureAuth(page, '/cpa')
    await expect(page.getByTestId('cpa-page-title')).toBeVisible()
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
    await expect.poll(() => providerCalls).toBe(callsBeforeRefresh + 1)

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
    failPatch = false

    await page.getByTestId('cpa-edit-instance').click()
    const editDialog = page.getByTestId('cpa-instance-dialog')
    await expect(editDialog.getByTestId('cpa-instance-secret')).toHaveValue('')
    await editDialog.getByTestId('cpa-instance-name').fill(`${instanceName} updated`)
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
})
