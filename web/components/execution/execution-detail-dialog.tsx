'use client';

import { useQuery } from '@tanstack/react-query';
import { executionApi, type ExecutionDetail, type TaskInstance } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { format, formatDistance } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { X, CheckCircle2, XCircle, Clock, PlayCircle, AlertCircle, ChevronDown, RotateCw } from 'lucide-react';
import { toast } from 'sonner';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import Editor from '@monaco-editor/react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import Link from 'next/link';

interface ExecutionDetailDialogProps {
  executionId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ExecutionDetailDialog({
  executionId,
  open,
  onOpenChange,
}: ExecutionDetailDialogProps) {
  const queryClient = useQueryClient();

  const { data: executionDetail, isLoading } = useQuery({
    queryKey: ['execution-detail', executionId],
    queryFn: () => executionApi.getDetails(executionId),
    enabled: open && !!executionId,
    refetchInterval: (query) => {
      const data = query.state.data as ExecutionDetail | undefined;
      if (!data) return false;
      const status = data.execution?.status;
      // Poll if status is PENDING or RUNNING, stop polling once it's COMPLETED, SUCCEEDED, or FAILED
      if (status === 'PENDING' || status === 'RUNNING') {
        return 2000; // Poll every 2 seconds
      }
      return false; // Stop polling for terminal states
    },
  });

  const cancelMutation = useMutation({
    mutationFn: () => executionApi.cancel(executionId, 'Cancelled by user'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['execution-detail', executionId] });
      queryClient.invalidateQueries({ queryKey: ['execution', executionId] });
      queryClient.invalidateQueries({ queryKey: ['executions'] });
      onOpenChange(false);
    },
  });

  // Get execution info early for retry mutation
  const execution = executionDetail?.execution;
  
  // Get failed nodes
  const failedNodes = executionDetail?.task_instances
    .filter((task) => task.state === 'FAILED')
    .map((task) => task.node_id) || [];
  const failedNodeId = failedNodes.length > 0 ? failedNodes[0] : null;
  const canRetry = (execution?.status === 'FAILED' || execution?.status === 'COMPLETED') && failedNodeId;

  const retryMutation = useMutation({
    mutationFn: () => {
      if (!failedNodeId) {
        throw new Error('No failed node found to retry from');
      }
      const currentExecution = executionDetail?.execution;
      if (!currentExecution) {
        throw new Error('Execution data not available');
      }
      return executionApi.retry(executionId, {
        node_id: failedNodeId,
        workflow_version: currentExecution.workflow_version,
      });
    },
    onSuccess: (data) => {
      toast.success('Workflow retry started');
      queryClient.invalidateQueries({ queryKey: ['executions'] });
      // Close current dialog and open the retried execution
      // The parent component should handle opening the new execution dialog
      onOpenChange(false);
    },
    onError: (error) => {
      toast.error('Failed to retry workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

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

  const getTaskStatusIcon = (state: string) => {
    switch (state) {
      case 'COMPLETED':
      case 'SUCCEEDED':
        return <CheckCircle2 className="h-4 w-4 text-green-500" />;
      case 'FAILED':
        return <XCircle className="h-4 w-4 text-red-500" />;
      case 'RUNNING':
        return <PlayCircle className="h-4 w-4 text-blue-500" />;
      case 'PENDING':
      case 'QUEUED':
        return <Clock className="h-4 w-4 text-yellow-500" />;
      default:
        return <AlertCircle className="h-4 w-4 text-gray-500" />;
    }
  };

  const getTaskStatusBadgeVariant = (state: string) => {
    switch (state) {
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

  const formatError = (error: any): string => {
    if (typeof error === 'string') return error;
    if (error?.message) return error.message;
    if (error?.error) return error.error;
    return JSON.stringify(error, null, 2);
  };

  if (!executionDetail && !isLoading) {
    return null;
  }

  const context = executionDetail?.context;
  const taskInstances = executionDetail?.task_instances || [];

  const canCancel = execution?.status === 'PENDING' || execution?.status === 'RUNNING';

  const duration = execution?.completed_at
    ? formatDistance(new Date(execution.completed_at), new Date(execution.created_at))
    : execution?.status === 'RUNNING'
    ? formatDistance(new Date(), new Date(execution.created_at))
    : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-7xl max-h-[95vh] flex flex-col">
        <DialogHeader className="pb-2">
          <div className="flex items-center justify-between pr-8">
            <div>
              <DialogTitle className="text-lg">Execution Details</DialogTitle>
              <DialogDescription className="text-xs mt-1">
                {execution?.id ? `Execution ID: ${execution.id}` : 'Loading...'}
              </DialogDescription>
            </div>
            {canCancel && (
              <Button
                variant="destructive"
                size="sm"
                onClick={() => cancelMutation.mutate()}
                disabled={cancelMutation.isPending}
                className="h-7 text-xs"
              >
                <X className="mr-1.5 h-3.5 w-3.5" />
                {cancelMutation.isPending ? 'Cancelling...' : 'Cancel'}
              </Button>
            )}
          </div>
        </DialogHeader>
        
        {/* Retry button overlapping with close button */}
        {canRetry && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => retryMutation.mutate()}
            disabled={retryMutation.isPending}
            className="absolute top-3 right-3 h-7 w-7 p-0 z-10"
            title={retryMutation.isPending ? 'Retrying...' : 'Retry'}
          >
            <RotateCw className={`h-3.5 w-3.5 ${retryMutation.isPending ? 'animate-spin' : ''}`} />
          </Button>
        )}

        {isLoading ? (
          <div className="flex items-center justify-center min-h-[300px]">
            <div className="text-center">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
              <p className="text-muted-foreground">Loading execution details...</p>
            </div>
          </div>
        ) : execution ? (
          <div className="flex-1 overflow-hidden flex flex-col gap-2 min-h-0">
            {/* Compact Overview Info */}
            <div className="flex flex-wrap items-center gap-3 text-xs border-b pb-2">
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">Status:</span>
                <Badge variant={getStatusBadgeVariant(execution.status)} className="text-sm">
                  {execution.status}
                </Badge>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">Duration:</span>
                <span>{duration || 'N/A'}</span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">Tasks:</span>
                <span>
                  {taskInstances.length} total
                  {taskInstances.filter((t) => t.state === 'FAILED').length > 0 && (
                    <span className="text-destructive ml-1">
                      ({taskInstances.filter((t) => t.state === 'FAILED').length} failed)
                    </span>
                  )}
                </span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">Workflow:</span>
                <Link
                  href={`/workflows/${execution.workflow_definition_id}`}
                  className="text-primary hover:underline font-mono"
                  onClick={(e) => e.stopPropagation()}
                >
                  {execution.workflow_definition_id}
                </Link>
                <span className="text-muted-foreground">v{execution.workflow_version}</span>
              </div>
            </div>

            <Tabs defaultValue="tasks" className="flex-1 overflow-hidden flex flex-col min-h-0">
              <TabsList className="h-8 flex-shrink-0">
                <TabsTrigger value="tasks" className="text-xs px-3 py-1">Task Execution</TabsTrigger>
                <TabsTrigger value="context" className="text-xs px-3 py-1">Context</TabsTrigger>
              </TabsList>

              {/* Task Execution Tab */}
              <TabsContent value="tasks" className="flex-1 overflow-auto min-h-0">
                {taskInstances.length === 0 ? (
                  <Card>
                    <CardContent className="pt-4">
                      <p className="text-center text-muted-foreground text-sm">No task instances found</p>
                    </CardContent>
                  </Card>
                ) : (
                  <div className="space-y-2">
                    {taskInstances.map((task) => (
                      <Card key={task.id} className="border">
                        <CardHeader className="pb-2 pt-2 px-3">
                          <div className="flex items-center gap-2 flex-wrap">
                            {getTaskStatusIcon(task.state)}
                            <span className="text-sm font-medium">{task.node_id}</span>
                            <span className="text-xs text-muted-foreground">•</span>
                            <span className="text-xs text-muted-foreground">{task.node_type}</span>
                            <span className="text-xs text-muted-foreground">•</span>
                            <span className="text-xs text-muted-foreground">Attempt {task.attempt}</span>
                            {task.completed_at && (
                              <>
                                <span className="text-xs text-muted-foreground">•</span>
                                <span className="text-xs text-muted-foreground">
                                  {format(new Date(task.completed_at), 'MMM d, HH:mm')}
                                </span>
                              </>
                            )}
                            {!task.completed_at && task.started_at && (
                              <>
                                <span className="text-xs text-muted-foreground">•</span>
                                <span className="text-xs text-muted-foreground">
                                  Started: {format(new Date(task.started_at), 'MMM d, HH:mm')}
                                </span>
                              </>
                            )}
                            <div className="ml-auto">
                              <Badge variant={getTaskStatusBadgeVariant(task.state)} className="text-xs">
                                {task.state}
                              </Badge>
                            </div>
                          </div>
                        </CardHeader>
                        <CardContent className="space-y-1 px-3 pb-2 pt-0">
                          {/* Error Display */}
                          {task.state === 'FAILED' && task.last_error && (
                            <div className="border border-destructive rounded-md p-2 bg-destructive/10">
                              <div className="flex items-center gap-1.5 mb-1.5">
                                <XCircle className="h-3.5 w-3.5 text-destructive" />
                                <h4 className="font-semibold text-destructive text-xs">Error</h4>
                              </div>
                              <pre className="text-xs overflow-auto whitespace-pre-wrap max-h-32">
                                {formatError(task.last_error)}
                              </pre>
                            </div>
                          )}

                          {/* Input/Output - Combined Collapsible */}
                          {(task.input || task.output) && (
                            <Collapsible>
                              <CollapsibleTrigger className="flex items-center justify-between w-full text-left py-0.5">
                                <span className="font-medium text-xs">
                                  {task.input && task.output ? 'Input / Output' : task.input ? 'Input' : 'Output'}
                                </span>
                                <ChevronDown className="h-3.5 w-3.5" />
                              </CollapsibleTrigger>
                              <CollapsibleContent className="mt-1">
                                <div className="grid gap-2 md:grid-cols-2">
                                  {task.input && (
                                    <div>
                                      <div className="text-xs font-medium mb-1 text-muted-foreground">Input</div>
                                      <div className="border rounded-md overflow-hidden" style={{ height: '150px' }}>
                                        <Editor
                                          height="150px"
                                          defaultLanguage="json"
                                          value={JSON.stringify(task.input, null, 2)}
                                          options={{
                                            readOnly: true,
                                            minimap: { enabled: false },
                                            fontSize: 11,
                                            lineNumbers: "off",
                                            wordWrap: "on",
                                            scrollBeyondLastLine: false,
                                          }}
                                          theme="vs-dark"
                                        />
                                      </div>
                                    </div>
                                  )}
                                  {task.output && (
                                    <div>
                                      <div className="text-xs font-medium mb-1 text-muted-foreground">Output</div>
                                      <div className="border rounded-md overflow-hidden" style={{ height: '150px' }}>
                                        <Editor
                                          height="150px"
                                          defaultLanguage="json"
                                          value={JSON.stringify(task.output, null, 2)}
                                          options={{
                                            readOnly: true,
                                            minimap: { enabled: false },
                                            fontSize: 11,
                                            lineNumbers: "off",
                                            wordWrap: "on",
                                            scrollBeyondLastLine: false,
                                          }}
                                          theme="vs-dark"
                                        />
                                      </div>
                                    </div>
                                  )}
                                </div>
                              </CollapsibleContent>
                            </Collapsible>
                          )}
                        </CardContent>
                      </Card>
                    ))}
                  </div>
                )}
              </TabsContent>

              {/* Context Tab */}
              <TabsContent value="context" className="flex-1 overflow-auto min-h-0">
                <Card>
                  <CardHeader className="pb-2 pt-3">
                    <CardTitle className="text-base">Workflow Context</CardTitle>
                    <CardDescription className="text-xs">
                      Runtime context including input, config, and task results
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="pt-2">
                    {context ? (
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                        {context.input && (
                          <div>
                            <h4 className="font-medium mb-1.5 text-sm">Input</h4>
                            <div className="border rounded-md overflow-hidden" style={{ height: '250px' }}>
                              <Editor
                                height="250px"
                                defaultLanguage="json"
                                value={JSON.stringify(context.input, null, 2)}
                                options={{
                                  readOnly: true,
                                  minimap: { enabled: false },
                                  fontSize: 11,
                                  lineNumbers: "off",
                                  wordWrap: "on",
                                  scrollBeyondLastLine: false,
                                }}
                                theme="vs-dark"
                              />
                            </div>
                          </div>
                        )}
                        {context.config && (
                          <div>
                            <h4 className="font-medium mb-1.5 text-sm">Config</h4>
                            <div className="border rounded-md overflow-hidden" style={{ height: '250px' }}>
                              <Editor
                                height="250px"
                                defaultLanguage="json"
                                value={JSON.stringify(context.config, null, 2)}
                                options={{
                                  readOnly: true,
                                  minimap: { enabled: false },
                                  fontSize: 11,
                                  lineNumbers: "off",
                                  wordWrap: "on",
                                  scrollBeyondLastLine: false,
                                }}
                                theme="vs-dark"
                              />
                            </div>
                          </div>
                        )}
                      </div>
                    ) : (
                      <p className="text-muted-foreground text-sm">No context data available</p>
                    )}
                  </CardContent>
                </Card>
              </TabsContent>
            </Tabs>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
