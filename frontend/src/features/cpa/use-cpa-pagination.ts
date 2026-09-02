import { useState } from 'react';

const CPA_TABLE_PAGE_SIZE_KEY = 'cpa-table-page-size';
export const CPA_TABLE_PAGE_SIZES = [10, 20, 30, 40, 50] as const;

export function clampTablePageSize(value: number): number {
  return (CPA_TABLE_PAGE_SIZES as readonly number[]).includes(value) ? value : 50;
}

function readTablePageSize(): number {
  try {
    return clampTablePageSize(Number(localStorage.getItem(CPA_TABLE_PAGE_SIZE_KEY)));
  } catch {
    return 50;
  }
}

function writeTablePageSize(value: number) {
  try {
    localStorage.setItem(CPA_TABLE_PAGE_SIZE_KEY, String(clampTablePageSize(value)));
  } catch {
    // Best-effort persistence; failing to store the preference is harmless.
  }
}

export function useCPAPagination() {
  const [pageSize, setPageSizeState] = useState(readTablePageSize);
  const [after, setAfter] = useState<string>();
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([]);

  const resetPagination = () => {
    setAfter(undefined);
    setCursorHistory([]);
  };

  const setPageSize = (value: number) => {
    const next = clampTablePageSize(value);
    setPageSizeState(next);
    writeTablePageSize(next);
    resetPagination();
  };

  return {
    pageSize,
    after,
    setAfter,
    cursorHistory,
    setCursorHistory,
    resetPagination,
    setPageSize,
  };
}
