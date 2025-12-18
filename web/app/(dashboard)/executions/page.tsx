'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { executionApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
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
import { Eye, RotateCw, Search } from 'lucide-react';
import { useState, useEffect } from 'react';
import { ExecutionDetailDialog } from '@/components/execution/execution-detail-dialog';
import { RetryExecutionDialog } from '@/components/execution/retry-execution-dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Input } from '@/components/ui/input';

export default function ExecutionsPage() {
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [workflowFilter, setWorkflowFilter] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [page, setPage] = useState(1);
  const [selectedExecutionId, setSelectedExecutionId] = useState<string | null>(null);
  const [detailDialogOpen, setDetailDialogOpen] = useState(false);
  const [retryExecutionId, setRetryExecutionId] = useState<string | null>(null);
  const [retryDialogOpen, setRetryDialogOpen] = useState(false);
  const pageSize = 20;
  const queryClient = useQueryClient();

  // Debounce search query
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearch(searchQuery);
      setPage(1); // Reset to first page when search changes
    }, 300);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Fetch execution details for retry
  const { data: retryExecutionDetail } = useQuery({
    queryKey: ['execution-detail', retryExecutionId],
    queryFn: () => retryExecutionId ? executionApi.getDetails(retryExecutionId) : null,
    enabled: retryDialogOpen && !!retryExecutionId,
  });

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
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading executions...</p>
        </div>
      </div>
    );
  }

  if (isError) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Executions</h1>
          <p className="text-muted-foreground">
            Monitor and manage workflow executions
          </p>
        </div>
        <Card>
          <CardContent className="pt-6">
            <div className="text-center py-12">
              <p className="text-destructive mb-2">Failed to load executions</p>
              <p className="text-sm text-muted-foreground mb-4">
                {error instanceof Error ? error.message : 'Unknown error occurred'}
              </p>
              <Button
                onClick={() => queryClient.invalidateQueries({ queryKey: ['executions'] })}
              >
                Retry
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
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
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Executions</h1>
        <p className="text-muted-foreground">
          Monitor and manage workflow executions
        </p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>All Executions</CardTitle>
              <CardDescription>
                {total} execution{total !== 1 ? 's' : ''} total
              </CardDescription>
            </div>
            <div className="flex gap-2">
              <div className="relative w-64">
                <Search className="absolute left-2 top-1/2 transform -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                <Input
                  placeholder="Search by execution ID or workflow ID..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="pl-8"
                />
              </div>
              <Select value={statusFilter} onValueChange={(value) => {
                setStatusFilter(value);
                setPage(1); // Reset to first page when filter changes
              }}>
                <SelectTrigger className="w-[180px]">
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
                className="w-[200px]"
              />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {executions.length === 0 ? (
            <div className="text-center py-12">
              <p className="text-muted-foreground">No executions found</p>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Execution ID</TableHead>
                    <TableHead>Workflow ID</TableHead>
                    <TableHead>Version</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Updated</TableHead>
                    <TableHead>Completed</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {executions.map((execution, index) => (
                    <TableRow key={`${execution.id}-${index}`}>
                      <TableCell className="font-mono text-sm">
                        {execution.id}
                      </TableCell>
                      <TableCell className="font-mono text-sm">
                        {execution.workflow_definition_id}
                      </TableCell>
                      <TableCell>v{execution.workflow_version}</TableCell>
                      <TableCell>
                        <Badge variant={getStatusBadgeVariant(execution.status)}>
                          {execution.status}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {execution.created_at
                          ? format(new Date(execution.created_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell>
                        {execution.updated_at
                          ? format(new Date(execution.updated_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell>
                        {execution.completed_at
                          ? format(new Date(execution.completed_at), 'MMM d, yyyy HH:mm')
                          : '-'}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          {(execution.status === 'FAILED' || execution.status === 'COMPLETED') && (
                            <Button
                              variant="ghost"
                              size="icon"
                              onClick={() => {
                                setRetryExecutionId(execution.id);
                                setRetryDialogOpen(true);
                              }}
                              title="Retry execution"
                            >
                              <RotateCw className="h-4 w-4" />
                            </Button>
                          )}
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => {
                              setSelectedExecutionId(execution.id);
                              setDetailDialogOpen(true);
                            }}
                            title="View details"
                          >
                            <Eye className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              {totalPages > 1 && (
                <div className="flex items-center justify-between mt-4">
                  <div className="text-sm text-muted-foreground">
                    Showing {executions.length > 0 ? (page - 1) * pageSize + 1 : 0} to{' '}
                    {Math.min(page * pageSize, total)} of {total} executions
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                      disabled={page === 1 || isLoading}
                    >
                      Previous
                    </Button>
                    <div className="text-sm text-muted-foreground flex items-center px-2">
                      Page {page} of {totalPages}
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                      disabled={page === totalPages || isLoading}
                    >
                      Next
                    </Button>
                  </div>
                </div>
              )}
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

      {retryExecutionDetail && (
        <RetryExecutionDialog
          executionDetail={retryExecutionDetail}
          open={retryDialogOpen}
          onOpenChange={(open) => {
            setRetryDialogOpen(open);
            if (!open) {
              setRetryExecutionId(null);
            }
          }}
        />
      )}
    </div>
  );
}
