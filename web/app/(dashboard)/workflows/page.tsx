'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { workflowApi, type WorkflowDefinition, type WorkflowIssueSummary } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Plus, Edit, Trash2, Play, Search, MoreVertical, AlertTriangle } from 'lucide-react';
import Link from 'next/link';
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { ScrollArea } from '@/components/ui/scroll-area';
import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { ExecuteWorkflowDialog } from '@/components/workflow/execute-workflow-dialog';
import { toast } from 'sonner';

export default function WorkflowsPage() {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [workflowToDelete, setWorkflowToDelete] = useState<WorkflowDefinition | null>(null);
  const [executeDialogOpen, setExecuteDialogOpen] = useState(false);
  const [workflowToExecute, setWorkflowToExecute] = useState<WorkflowDefinition | null>(null);
  const [issueSummaryDialogOpen, setIssueSummaryDialogOpen] = useState(false);
  const [workflowForIssueSummary, setWorkflowForIssueSummary] = useState<WorkflowDefinition | null>(null);
  const [page, setPage] = useState(1);
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const pageSize = 10;
  const router = useRouter();
  const queryClient = useQueryClient();

  // Debounce search query
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearch(searchQuery);
      setPage(1); // Reset to first page when search changes
    }, 300);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  const { data: workflowResponse, isLoading, error, isError } = useQuery({
    queryKey: ['workflows', page, debouncedSearch],
    queryFn: () => workflowApi.list(undefined, debouncedSearch || undefined, page, pageSize),
    retry: 2,
  });

  // Issue summary query - must be called before early returns
  const { data: issueSummary, isLoading: isLoadingIssueSummary } = useQuery({
    queryKey: ['workflow-issue-summary', workflowForIssueSummary?.id],
    queryFn: () => workflowForIssueSummary ? workflowApi.getIssueSummary(workflowForIssueSummary.id) : null,
    enabled: issueSummaryDialogOpen && !!workflowForIssueSummary,
    retry: 1,
  });

  // Handle both old format (array) and new format (object) for backward compatibility
  const workflows = Array.isArray(workflowResponse) 
    ? workflowResponse 
    : workflowResponse?.workflows || [];
  const totalPages = workflowResponse?.total_pages || (workflows.length > 0 ? 1 : 0);
  const total = workflowResponse?.total || workflows.length;

  const deleteMutation = useMutation({
    mutationFn: (workflow: WorkflowDefinition) => workflowApi.delete(workflow.id, workflow.version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setDeleteDialogOpen(false);
      setWorkflowToDelete(null);
      toast.success('Workflow deleted successfully');
    },
    onError: (error) => {
      toast.error('Failed to delete workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const executeMutation = useMutation({
    mutationFn: async (data: {
      id: string;
      version: number;
      input: Record<string, any>;
      execution_options?: Record<string, any>;
    }) => {
      return workflowApi.execute(data.id, {
        workflow_version: data.version,
        input: data.input,
        execution_options: data.execution_options,
      });
    },
    onSuccess: (data) => {
      toast.success('Workflow execution started');
      setExecuteDialogOpen(false);
      setWorkflowToExecute(null);
      router.push(`/executions/${data.run_id}`);
    },
    onError: (error) => {
      toast.error('Failed to execute workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading workflows...</p>
        </div>
      </div>
    );
  }

  if (isError) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold tracking-tight">Workflows</h1>
            <p className="text-muted-foreground">
              Manage and monitor your workflow definitions
            </p>
          </div>
        </div>
        <Card>
          <CardContent className="pt-6">
            <div className="text-center py-12">
              <p className="text-destructive mb-2">Failed to load workflows</p>
              <p className="text-sm text-muted-foreground mb-4">
                {error instanceof Error ? error.message : 'Unknown error occurred'}
              </p>
              <Button
                onClick={() => queryClient.invalidateQueries({ queryKey: ['workflows'] })}
              >
                Retry
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }


  const handleDelete = (workflow: WorkflowDefinition) => {
    setWorkflowToDelete(workflow);
    setDeleteDialogOpen(true);
  };

  const confirmDelete = () => {
    if (workflowToDelete) {
      deleteMutation.mutate(workflowToDelete);
    }
  };

  const handleExecute = (workflow: WorkflowDefinition) => {
    setWorkflowToExecute(workflow);
    setExecuteDialogOpen(true);
  };

  const handleExecuteConfirm = async (data: {
    input: Record<string, any>;
    execution_options?: Record<string, any>;
  }) => {
    if (!workflowToExecute) return;
    await executeMutation.mutateAsync({
      id: workflowToExecute.id,
      version: workflowToExecute.version || 1,
      input: data.input,
      execution_options: data.execution_options,
    });
  };

  const handleViewIssueSummary = (workflow: WorkflowDefinition) => {
    setWorkflowForIssueSummary(workflow);
    setIssueSummaryDialogOpen(true);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Workflows</h1>
          <p className="text-muted-foreground">
            Manage and monitor your workflow definitions
          </p>
        </div>
        <Link href="/workflows/new">
          <Button>
            <Plus className="mr-2 h-4 w-4" />
            Create Workflow
          </Button>
        </Link>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>All Workflows</CardTitle>
              <CardDescription>
                {total} workflow{total !== 1 ? 's' : ''} total
              </CardDescription>
            </div>
            <div className="relative w-64">
              <Search className="absolute left-2 top-1/2 transform -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                placeholder="Search workflows..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="pl-8"
              />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {workflows.length === 0 ? (
            <div className="text-center py-12">
              <p className="text-muted-foreground mb-4">No workflows found</p>
              <Link href="/workflows/new">
                <Button>
                  <Plus className="mr-2 h-4 w-4" />
                  Create Your First Workflow
                </Button>
              </Link>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>ID</TableHead>
                    <TableHead>Version</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Success Rate</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {workflows.map((workflow, index) => (
                    <TableRow key={`${workflow.id}-${index}`}>
                      <TableCell className="font-medium">
                        {workflow.name}
                      </TableCell>
                      <TableCell className="font-mono text-sm">
                        {workflow.id}
                      </TableCell>
                      <TableCell>v{workflow.version}</TableCell>
                      <TableCell>
                        <Badge variant={workflow.enabled ? 'default' : 'secondary'}>
                          {workflow.enabled ? 'Enabled' : 'Disabled'}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {workflow.success_rate !== undefined ? (
                          <Badge
                            variant={
                              workflow.success_rate >= 95
                                ? 'default'
                                : workflow.success_rate >= 80
                                ? 'secondary'
                                : 'destructive'
                            }
                          >
                            {workflow.success_rate.toFixed(1)}%
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground">N/A</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {format(new Date(workflow.created_at), 'MMM d, yyyy')}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center justify-end gap-2">
                          <Button
                            variant="ghost"
                            size="sm"
                            asChild
                            title="Edit workflow"
                          >
                            <Link href={`/workflows/${workflow.id}/edit`}>
                              <Edit className="h-4 w-4" />
                            </Link>
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleExecute(workflow)}
                            title="Execute workflow"
                            disabled={executeMutation.isPending || executeDialogOpen}
                          >
                            <Play className="h-4 w-4" />
                          </Button>
                          {workflow.success_rate !== undefined && workflow.success_rate < 95 && (
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  title="View issue summary"
                                >
                                  <AlertTriangle className="h-4 w-4 text-destructive" />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                <DropdownMenuItem
                                  onClick={() => handleViewIssueSummary(workflow)}
                                >
                                  <AlertTriangle className="mr-2 h-4 w-4" />
                                  View Issue Summary
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          )}
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleDelete(workflow)}
                            title="Delete workflow"
                            className="text-destructive hover:text-destructive"
                          >
                            <Trash2 className="h-4 w-4" />
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
                    Showing {workflows.length > 0 ? (page - 1) * pageSize + 1 : 0} to{' '}
                    {Math.min(page * pageSize, total)} of {total} workflows
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

      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Workflow</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete &quot;{workflowToDelete?.name}&quot;? This action
              cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDeleteDialogOpen(false)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={confirmDelete}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {workflowToExecute && (
        <ExecuteWorkflowDialog
          open={executeDialogOpen}
          onOpenChange={(open) => {
            setExecuteDialogOpen(open);
            if (!open) {
              setWorkflowToExecute(null);
            }
          }}
          workflowId={workflowToExecute.id}
          workflowVersion={workflowToExecute.version || 1}
          onExecute={handleExecuteConfirm}
          isExecuting={executeMutation.isPending}
        />
      )}

      <Dialog open={issueSummaryDialogOpen} onOpenChange={setIssueSummaryDialogOpen}>
        <DialogContent className="max-w-3xl max-h-[80vh] flex flex-col">
          <DialogHeader>
            <DialogTitle>Issue Summary</DialogTitle>
            <DialogDescription>
              {workflowForIssueSummary
                ? `Analysis for workflow: ${workflowForIssueSummary.name} (${workflowForIssueSummary.id})`
                : 'Loading...'}
            </DialogDescription>
          </DialogHeader>
          <div className="flex-1 overflow-hidden">
            {isLoadingIssueSummary ? (
              <div className="flex items-center justify-center min-h-[200px]">
                <div className="text-center">
                  <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
                  <p className="text-muted-foreground">Analyzing issues...</p>
                </div>
              </div>
            ) : issueSummary ? (
              <div className="space-y-4">
                <div>
                  <h4 className="font-medium mb-2">Summary</h4>
                  <div className="bg-muted rounded-md p-4">
                    <p className="text-sm whitespace-pre-wrap">{issueSummary.summary}</p>
                  </div>
                </div>
                {issueSummary.errors.length > 0 && (
                  <div>
                    <h4 className="font-medium mb-2">Errors ({issueSummary.errors.length})</h4>
                    <ScrollArea className="h-[300px] border rounded-md p-4">
                      <div className="space-y-2">
                        {issueSummary.errors.map((error, index) => (
                          <div
                            key={index}
                            className="text-sm bg-destructive/10 border border-destructive/20 rounded p-2"
                          >
                            <pre className="whitespace-pre-wrap text-xs">{error}</pre>
                          </div>
                        ))}
                      </div>
                    </ScrollArea>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-muted-foreground">No issue summary available</p>
            )}
          </div>
          <DialogFooter>
            <Button onClick={() => setIssueSummaryDialogOpen(false)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
