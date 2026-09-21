export interface SelectedExecution<T extends { id: string }> {
  execution: T;
  index: number;
}

export function selectExecution<T extends { id: string }>(
  executions: readonly T[],
  selectedExecutionId: string | null
): SelectedExecution<T> | null {
  if (executions.length === 0) {
    return null;
  }

  const selectedIndex = selectedExecutionId ? executions.findIndex((execution) => execution.id === selectedExecutionId) : -1;
  const index = selectedIndex >= 0 ? selectedIndex : executions.length - 1;

  return {
    execution: executions[index],
    index,
  };
}

export function hasRecordedJsonValue(value: unknown): boolean {
  if (value === null || value === undefined) {
    return false;
  }

  if (Array.isArray(value)) {
    return true;
  }

  if (typeof value === 'object') {
    return Object.keys(value).length > 0;
  }

  return true;
}

export function formatJsonValue(value: unknown): string {
  if (value === null || value === undefined) {
    return '';
  }

  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}
