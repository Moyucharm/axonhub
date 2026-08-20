import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import CPAManagement from '@/features/cpa';

function ProtectedCPA() {
  return (
    <RouteGuard requiredScopes={['read_settings']} scopeLevel='system'>
      <CPAManagement />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/cpa/')({
  component: ProtectedCPA,
});
