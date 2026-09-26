// Dev-only visual harness for the CPA credential table (quota cells and the
// Codex reset-credit panel). Not referenced by the app: open
// /__quota_preview__.html with the dev server to inspect the rendering.
import { StrictMode, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router';
import { ThemeProvider } from './context/theme-context';
import './index.css';
import './lib/i18n';
import { CPACredentialTable } from './features/cpa/components/credential-table';
import type { CPACredential, CPAQuotaItem, CPAQuotaResetCredit } from './features/cpa/types';

const at = (hours: number) => new Date(Date.now() + hours * 3600_000).toISOString();

const item = (over: Partial<CPAQuotaItem> & Pick<CPAQuotaItem, 'id' | 'label'>): CPAQuotaItem => ({
  group: '',
  description: '',
  unit: '',
  ...over,
});

function credential(
  id: number,
  provider: string,
  email: string,
  planType: string,
  items: CPAQuotaItem[],
  resetCredits: CPAQuotaResetCredit[] = []
): CPACredential {
  return {
    id,
    instanceID: 1,
    remoteName: `${provider}-${id}.json`,
    displayName: email,
    provider,
    email,
    status: 'active',
    statusMessage: '',
    disabled: false,
    unavailable: false,
    runtimeOnly: false,
    priority: 0,
    planType,
    quotaState: 'success',
    quotaData: { items, resetCredits, resetCreditsFailed: false },
    quotaLastAttemptAt: at(-0.1),
    quotaLastSuccessAt: at(-0.1),
    quotaLastFailureAt: null,
    quotaLastError: '',
    available: true,
    abnormal: false,
    stale: false,
    expired: false,
    cooling: false,
    cooldownUntil: null,
    createdAt: at(-100),
    updatedAt: at(-0.1),
  };
}

const credentials: CPACredential[] = [
  credential(1, 'codex', 'azusacc@proton.me', 'team', [
    item({ id: 'code-primary', group: 'Code', label: '5 hour', usedPercent: 0, remainingPercent: 100, periodSeconds: 18000, resetAt: at(4.7) }),
    item({
      id: 'code-secondary',
      group: 'Code',
      label: '7 day',
      usedPercent: 81,
      remainingPercent: 19,
      periodSeconds: 604800,
      resetAt: at(70),
      estimatedLimitUSD: 60.1,
      estimatedCostUSD: 48.7,
    }),
  ], [
    { id: 'rc-1', title: 'Full reset (Weekly + 5 hr)', resetType: 'codex_rate_limits', grantedAt: at(-200), expiresAt: at(30) },
    { id: 'rc-2', title: 'Full reset (Weekly + 5 hr)', resetType: 'codex_rate_limits', grantedAt: at(-100), expiresAt: at(240) },
    { id: 'rc-manual', title: 'Manual-only credit', resetType: 'codex_rate_limits', grantedAt: at(-50), expiresAt: null },
  ]),
  credential(2, 'antigravity', 'gemini-user@example.com', 'pro', [
    item({ id: 'g-5h', group: 'Gemini Models', label: '5 hour', usedPercent: 62, remainingPercent: 38, periodSeconds: 18000, resetAt: at(2) }),
    item({ id: 'g-7d', group: 'Gemini Models', label: '7 day', usedPercent: 12, remainingPercent: 88, periodSeconds: 604800, resetAt: at(120) }),
    item({ id: 'c-5h', group: 'Claude and GPT models', label: '5 hour', usedPercent: 96, remainingPercent: 4, periodSeconds: 18000, resetAt: at(0.5) }),
    item({ id: 'c-7d', group: 'Claude and GPT models', label: '7 day', usedPercent: 45, remainingPercent: 55, periodSeconds: 604800, resetAt: at(90) }),
  ]),
  credential(3, 'kimi', 'kimi-user@example.com', '', [
    item({
      id: 'k-weekly',
      group: 'Products',
      label: 'Weekly credits',
      usedPercent: 30,
      remainingPercent: 70,
      used: 3000,
      limit: 10000,
      unit: 'credits',
      resetAt: at(50),
      description: 'Weekly coding allowance shared by every Kimi product surface, including the CLI, IDE plugins and the web console.',
    }),
  ]),
];

function Preview() {
  const noop = () => {};
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set([1, 2, 3]));
  return (
    <div className='bg-background flex h-screen flex-col gap-4 p-6'>
      <CPACredentialTable
        credentials={credentials}
        pageInfo={{ hasNextPage: false, hasPreviousPage: false }}
        totalCount={credentials.length}
        isLoading={false}
        pageSize={50}
        expanded={expanded}
        canWrite
        instanceEnabled
        resetPending={false}
        refreshPending={false}
        togglePending={false}
        locale='zh-CN'
        onToggleExpanded={(id) =>
          setExpanded((prev) => {
            const next = new Set(prev);
            if (next.has(id)) {
              next.delete(id);
            } else {
              next.add(id);
            }
            return next;
          })
        }
        onRefreshCredential={noop}
        onRequestToggle={noop}
        onRequestReset={noop}
        onNextPage={noop}
        onPreviousPage={noop}
        onPageSizeChange={noop}
        onResetCursor={noop}
      />
    </div>
  );
}

const router = createRouter({ routeTree: createRootRoute({ component: Preview }), history: createMemoryHistory() });

ReactDOM.createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider defaultTheme='system' defaultColorScheme='claude'>
      <RouterProvider router={router} />
    </ThemeProvider>
  </StrictMode>
);
