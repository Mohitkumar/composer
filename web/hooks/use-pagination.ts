import { useState, useCallback } from 'react';

interface UsePaginationProps {
  initialPage?: number;
  pageSize?: number;
  onPageChange?: (page: number) => void;
}

export function usePagination({
  initialPage = 1,
  pageSize = 10,
  onPageChange,
}: UsePaginationProps = {}) {
  const [page, setPage] = useState(initialPage);

  const handlePageChange = useCallback(
    (newPage: number) => {
      setPage(newPage);
      onPageChange?.(newPage);
    },
    [onPageChange]
  );

  const resetPage = useCallback(() => {
    setPage(1);
  }, []);

  return {
    page,
    pageSize,
    setPage: handlePageChange,
    resetPage,
  };
}
