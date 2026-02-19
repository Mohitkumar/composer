'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { workflowApi, type WorkflowDefinition, type WorkflowIssueSummary } from '@/lib/api-client';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Plus, Edit, Trash2, Play, MoreVertical, AlertTriangle, Rocket, BarChart3, Network } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
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
import { ScrollArea } from '@/components/ui/scroll-area';
import { useState, useEffect, useMemo } from 'react';
import { ExecuteWorkflowDialog } from '@/components/workflow/execute-workflow-dialog';
import { ExecutionDetailDialog } from '@/components/execution/execution-detail-dialog';
import { WorkflowGraphDialog } from '@/components/workflow/workflow-graph-dialog';
import { toast } from 'sonner';
import { useDebounce } from '@/hooks/use-debounce';
import { ErrorState, LoadingState, PageHeader, Pagination, SearchInput } from '@/components/shared';

export default function WorkflowsPage() {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [workflowToDelete, setWorkflowToDelete] = useState<WorkflowDefinition | null>(null);
  const [executeDialogOpen, setExecuteDialogOpen] = useState(false);
  const [workflowToExecute, setWorkflowToExecute] = useState<WorkflowDefinition | null>(null);
  const [issueSummaryDialogOpen, setIssueSummaryDialogOpen] = useState(false);
  const [workflowForIssueSummary, setWorkflowForIssueSummary] = useState<WorkflowDefinition | null>(null);
  const [executionDetailDialogOpen, setExecutionDetailDialogOpen] = useState(false);
  const [executionId, setExecutionId] = useState<string | null>(null);
  const [graphDialogOpen, setGraphDialogOpen] = useState(false);
  const [workflowForGraph, setWorkflowForGraph] = useState<WorkflowDefinition | null>(null);
  const [page, setPage] = useState(1);
  const [searchQuery, setSearchQuery] = useState('');
  const debouncedSearch = useDebounce(searchQuery, 300);
  const pageSize = 10;
  const queryClient = useQueryClient();

  useEffect(() => {
    setPage(1); // Reset to first page when search changes
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
    mutationFn: (workflow: WorkflowDefinition) => workflowApi.delete(workflow.id, 0), // 0 means delete all versions
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setDeleteDialogOpen(false);
      setWorkflowToDelete(null);
      toast.success('Workflow and all versions deleted successfully');
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
        workflow_version: data.version || 0, // 0 means use deployed version
        input: data.input,
        execution_options: data.execution_options,
      });
    },
    onSuccess: (data) => {
      toast.success('Workflow execution started');
      setExecuteDialogOpen(false);
      setWorkflowToExecute(null);
      setExecutionId(data.run_id);
      setExecutionDetailDialogOpen(true);
      queryClient.invalidateQueries({ queryKey: ['executions'] });
    },
    onError: (error) => {
      toast.error('Failed to execute workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  // Helper to check if latest version is already deployed
  const isLatestVersionDeployed = (workflow: WorkflowDefinition): boolean => {
    if (!workflow.latest_version || !workflow.deployed_version) return false;
    return workflow.latest_version === workflow.deployed_version;
  };

  const deployMutation = useMutation({
    mutationFn: async (workflow: WorkflowDefinition) => {
      // Get the latest version to deploy (not the deployed version shown in list)
      const latestWorkflow = await workflowApi.get(workflow.id, undefined, true);
      if (latestWorkflow.deployed) {
        throw new Error('Latest version is already deployed');
      }
      await workflowApi.deploy(workflow.id, latestWorkflow.version);
    },
    onSuccess: (_, workflow) => {
      // Invalidate all queries related to this workflow and the workflows list
      queryClient.invalidateQueries({ queryKey: ['workflow', workflow.id] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflow.id, 'latest'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflow.id, 'deployed'] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      toast.success('Workflow deployed successfully');
    },
    onError: (error) => {
      toast.error('Failed to deploy workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return <LoadingState message="Loading workflows..." />;
  }

  if (isError) {
    return (
      <ErrorState
        title="Workflows"
        description="Manage and monitor your workflow definitions"
        error={error}
        queryKey={['workflows']}
      />
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
      version: 0, // 0 means use deployed version
      input: data.input,
      execution_options: data.execution_options,
    });
  };

  const handleViewIssueSummary = (workflow: WorkflowDefinition) => {
    setWorkflowForIssueSummary(workflow);
    setIssueSummaryDialogOpen(true);
  };

  const handleViewGraph = (workflow: WorkflowDefinition) => {
    setWorkflowForGraph(workflow);
    setGraphDialogOpen(true);
  };

  return (
    <>
      <Card>
        <PageHeader
          title="Workflows"
          description={`${total} workflow${total !== 1 ? 's' : ''} total • Manage and monitor your workflow definitions`}
          action={
            <>
              <SearchInput
                placeholder="Search workflows..."
                value={searchQuery}
                onChange={setSearchQuery}
              />
              <Link href="/workflows/new">
                <Button size="sm" className="btn-small">
                  <Plus className="mr-1.5 h-3.5 w-3.5" />
                  Create Workflow
                </Button>
              </Link>
            </>
          }
        />
        <CardContent className="page-content-padding">
          {workflows.length === 0 ? (
            <div className="text-center py-12">
              <p className="text-muted-foreground mb-4 text-sm">No workflows found</p>
              <Link href="/workflows/new">
                <Button size="sm" className="btn-small">
                  <Plus className="mr-1.5 h-3.5 w-3.5" />
                  Create Your First Workflow
                </Button>
              </Link>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="table-header">Name</TableHead>
                    <TableHead className="table-header">ID</TableHead>
                    <TableHead className="table-header">Deployed Version</TableHead>
                    <TableHead className="table-header">Latest Version</TableHead>
                    <TableHead className="table-header">Status</TableHead>
                    <TableHead className="table-header">Success Rate</TableHead>
                    <TableHead className="table-header">Created</TableHead>
                    <TableHead className="table-header text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {workflows.map((workflow, index) => (
                    <TableRow key={`${workflow.id}-${index}`} className="table-row">
                      <TableCell className="table-cell text-sm font-medium">
                        {workflow.name}
                      </TableCell>
                      <TableCell className="table-cell font-mono text-xs">
                        {workflow.id}
                      </TableCell>
                      <TableCell className="table-cell text-sm">
                        {workflow.deployed_version
                          ? `v${workflow.deployed_version}`
                          : <span className="text-muted-foreground">-</span>}
                      </TableCell>
                      <TableCell className="table-cell text-sm">
                        {workflow.latest_version
                          ? `v${workflow.latest_version}`
                          : <span className="text-muted-foreground">-</span>}
                      </TableCell>
                      <TableCell className="table-cell">
                        <Badge variant={workflow.enabled ? 'default' : 'secondary'} className="text-xs">
                          {workflow.enabled ? 'Enabled' : 'Disabled'}
                        </Badge>
                      </TableCell>
                      <TableCell className="table-cell">
                        {workflow.success_rate !== undefined && workflow.success_rate >= 0 ? (
                          <Badge
                            variant={
                              workflow.success_rate >= 95
                                ? 'default'
                                : workflow.success_rate >= 80
                                ? 'secondary'
                                : 'destructive'
                            }
                            className="text-xs"
                          >
                            {workflow.success_rate.toFixed(1)}%
                          </Badge>
                        ) : (
                          <span className="text-xs text-muted-foreground">-</span>
                        )}
                      </TableCell>
                      <TableCell className="table-cell text-xs text-muted-foreground">
                        {format(new Date(workflow.created_at), 'MMM d, yyyy')}
                      </TableCell>
                      <TableCell className="table-cell">
                        <div className="flex items-center justify-end gap-1">
                          {/* Main menu: Edit, Run, Report */}
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            asChild
                            title="Edit workflow"
                          >
                            <Link href={`/workflows/${workflow.id}/edit`}>
                              <Edit className="h-3.5 w-3.5" />
                            </Link>
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            onClick={() => handleExecute(workflow)}
                            title={!workflow.deployed_version ? "No deployed version" : "Execute workflow"}
                            disabled={executeMutation.isPending || executeDialogOpen || !workflow.deployed_version}
                          >
                            <Play className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            asChild
                            title="View report"
                          >
                            <Link href={`/?workflow_id=${workflow.id}`}>
                              <BarChart3 className="h-3.5 w-3.5" />
                            </Link>
                          </Button>
                          
                          {/* Extended menu: Graph, Deploy, Issue Summary, Delete */}
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 w-7 p-0"
                                title="More options"
                              >
                                <MoreVertical className="h-3.5 w-3.5" />
                              </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                              <DropdownMenuItem
                                onClick={() => handleViewGraph(workflow)}
                              >
                                <Network className="mr-2 h-4 w-4" />
                                View Graph
                              </DropdownMenuItem>
                              <DropdownMenuSeparator />
                              <DropdownMenuItem
                                onClick={() => deployMutation.mutate(workflow)}
                                disabled={deployMutation.isPending || isLatestVersionDeployed(workflow)}
                              >
                                <Rocket className="mr-2 h-4 w-4" />
                                Deploy Latest Version
                              </DropdownMenuItem>
                              {workflow.success_rate !== undefined && workflow.success_rate >= 0 && workflow.success_rate < 95 && (
                                <DropdownMenuItem
                                  onClick={() => handleViewIssueSummary(workflow)}
                                >
                                  <AlertTriangle className="mr-2 h-4 w-4 text-destructive" />
                                  View Issue Summary
                                </DropdownMenuItem>
                              )}
                              <DropdownMenuSeparator />
                              <DropdownMenuItem
                                onClick={() => handleDelete(workflow)}
                                variant="destructive"
                              >
                                <Trash2 className="mr-2 h-4 w-4" />
                                Delete Workflow
                              </DropdownMenuItem>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </div>
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
                itemsCount={workflows.length}
                onPageChange={setPage}
                isLoading={isLoading}
              />
            </>
          )}
        </CardContent>
      </Card>

      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Workflow</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete &quot;{workflowToDelete?.name}&quot;? This will delete all versions of this workflow. This action
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
          workflowVersion={0}
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

      {executionId && (
        <ExecutionDetailDialog
          executionId={executionId}
          open={executionDetailDialogOpen}
          onOpenChange={(open) => {
            setExecutionDetailDialogOpen(open);
            if (!open) {
              setExecutionId(null);
            }
          }}
        />
      )}

      {workflowForGraph && (
        <WorkflowGraphDialog
          workflowId={workflowForGraph.id}
          open={graphDialogOpen}
          onOpenChange={(open) => {
            setGraphDialogOpen(open);
            if (!open) {
              setWorkflowForGraph(null);
            }
          }}
        />
      )}
    </>
  );
}
