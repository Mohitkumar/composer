'use client';

import { useQuery } from '@tanstack/react-query';
import { executionApi } from '@/lib/api-client';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { format } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Eye } from 'lucide-react';
import { useState, useEffect } from 'react';
import { ExecutionDetailDialog } from '@/components/execution/execution-detail-dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Input } from '@/components/ui/input';
import { ErrorState, LoadingState, PageHeader, Pagination, SearchInput } from '@/components/shared';
import { useDebounce } from '@/hooks/use-debounce';

export default function ExecutionsPage() {
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [workflowFilter, setWorkflowFilter] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState('');
  const debouncedSearch = useDebounce(searchQuery, 300);
  const [page, setPage] = useState(1);
  const [selectedExecutionId, setSelectedExecutionId] = useState<string | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const pageSize = 20;

  useEffect(() => {
    setPage(1); // Reset to first page when search changes
  }, [searchQuery]);

  const { data: executionResponse, isLoading, error, isError } = useQuery({
    queryKey: ['executions', statusFilter, workflowFilter, debouncedSearch, page],
    queryFn: () =>
      executionApi.list({
        status: statusFilter !== 'all' ? statusFilter : undefined,
        workflow_id: workflowFilter || undefined,
        search: debouncedSearch || undefined,
        page,
        page_size: pageSize,
      }),
    retry: 2,
  });

  // Handle both old format (array) and new format (object) for backward compatibility
  const executions = Array.isArray(executionResponse)
    ? executionResponse
    : executionResponse?.executions || [];
  const totalPages = executionResponse?.total_pages || (executions.length > 0 ? 1 : 0);
  const total = executionResponse?.total || executions.length;

  if (isLoading) {
    return <LoadingState message="Loading executions..." />;
  }

  if (isError) {
    return (
      <ErrorState
        title="Executions"
        description="Monitor and manage workflow executions"
        error={error}
        queryKey={['executions']}
      />
    );
  }


  const getStatusBadgeVariant = (status: string) => {
    switch (status) {
      case 'COMPLETED':
      case 'SUCCEEDED':
        return 'default';
      case 'FAILED':
        return 'destructive';
      case 'RUNNING':
        return 'secondary';
      default:
        return 'outline';
    }
  };

  return (
    <>
      <Card>
        <PageHeader
          title="Executions"
          description={`${total} execution${total !== 1 ? 's' : ''} total • Monitor and manage workflow executions`}
          action={
            <>
              <SearchInput
                placeholder="Search by execution ID or workflow ID..."
                value={searchQuery}
                onChange={setSearchQuery}
              />
              <Select value={statusFilter} onValueChange={(value) => {
                setStatusFilter(value);
                setPage(1); // Reset to first page when filter changes
              }}>
                <SelectTrigger className="w-[160px] input-small">
                  <SelectValue placeholder="Filter by status" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All Statuses</SelectItem>
                  <SelectItem value="PENDING">Pending</SelectItem>
                  <SelectItem value="RUNNING">Running</SelectItem>
                  <SelectItem value="COMPLETED">Completed</SelectItem>
                  <SelectItem value="SUCCEEDED">Succeeded</SelectItem>
                  <SelectItem value="FAILED">Failed</SelectItem>
                </SelectContent>
              </Select>
              <Input
                placeholder="Filter by workflow ID"
                value={workflowFilter}
                onChange={(e) => {
                  setWorkflowFilter(e.target.value);
                  setPage(1); // Reset to first page when filter changes
                }}
                className="w-[180px] input-small"
              />
            </>
          }
        />
        <CardContent className="page-content-padding">
          {executions.length === 0 ? (
            <div className="text-center py-12">
              <p className="text-muted-foreground text-sm">No executions found</p>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="table-header">Execution ID</TableHead>
                    <TableHead className="table-header">Workflow ID</TableHead>
                    <TableHead className="table-header">Version</TableHead>
                    <TableHead className="table-header">Status</TableHead>
                    <TableHead className="table-header">Created</TableHead>
                    <TableHead className="table-header">Updated</TableHead>
                    <TableHead className="table-header">Completed</TableHead>
                    <TableHead className="table-header text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {executions.map((execution, index) => (
                    <TableRow key={`${execution.id}-${index}`} className="table-row">
                      <TableCell className="table-cell font-mono text-xs">
                        {execution.id}
                      </TableCell>
                      <TableCell className="table-cell font-mono text-xs">
                        {execution.workflow_definition_id}
                      </TableCell>
                      <TableCell className="table-cell text-sm">v{execution.workflow_version}</TableCell>
                      <TableCell className="table-cell">
                        <Badge variant={getStatusBadgeVariant(execution.status)} className="text-xs">
                          {execution.status}
                        </Badge>
                      </TableCell>
                      <TableCell className="table-cell text-xs text-muted-foreground">
                        {execution.created_at
                          ? format(new Date(execution.created_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell className="table-cell text-xs text-muted-foreground">
                        {execution.updated_at
                          ? format(new Date(execution.updated_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell className="table-cell text-xs text-muted-foreground">
                        {execution.completed_at
                          ? format(new Date(execution.completed_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell className="table-cell text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0"
                          onClick={() => {
                            setSelectedExecutionId(execution.id);
                            setDetailDialogOpen(true);
                          }}
                          title="View details"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              <Pagination
                currentPage={page}
                totalPages={totalPages}
                total={total}
                pageSize={pageSize}
                itemsCount={executions.length}
                onPageChange={setPage}
                isLoading={isLoading}
              />
            </>
          )}
        </CardContent>
      </Card>

      {selectedExecutionId && (
        <ExecutionDetailDialog
          executionId={selectedExecutionId}
          open={detailDialogOpen}
          onOpenChange={(open) => {
            setDetailDialogOpen(open);
            if (!open) {
              setSelectedExecutionId(null);
            }
          }}
        />
      )}
    </>
  );
}
