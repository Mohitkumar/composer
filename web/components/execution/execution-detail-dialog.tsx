'use client';

import { useQuery } from '@tanstack/react-query';
import { executionApi, type ExecutionDetail, type TaskInstance } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { format, formatDistance } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { X, CheckCircle2, XCircle, Clock, PlayCircle, AlertCircle, ChevronDown } from 'lucide-react';
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

  const execution = executionDetail?.execution;
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
        <DialogHeader>
          <div className="flex items-center justify-between">
            <div>
              <DialogTitle>Execution Details</DialogTitle>
              <DialogDescription>
                {execution?.id ? `Execution ID: ${execution.id}` : 'Loading...'}
              </DialogDescription>
            </div>
            {canCancel && (
              <Button
                variant="destructive"
                size="sm"
                onClick={() => cancelMutation.mutate()}
                disabled={cancelMutation.isPending}
              >
                <X className="mr-2 h-4 w-4" />
                {cancelMutation.isPending ? 'Cancelling...' : 'Cancel'}
              </Button>
            )}
          </div>
        </DialogHeader>

        {isLoading ? (
          <div className="flex items-center justify-center min-h-[400px]">
            <div className="text-center">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
              <p className="text-muted-foreground">Loading execution details...</p>
            </div>
          </div>
        ) : execution ? (
          <div className="flex-1 overflow-hidden flex flex-col gap-4">
            {/* Compact Overview Info */}
            <div className="flex flex-wrap items-center gap-4 text-sm border-b pb-3">
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

            <Tabs defaultValue="tasks" className="flex-1 overflow-hidden flex flex-col">
              <TabsList>
                <TabsTrigger value="tasks">Task Execution</TabsTrigger>
                <TabsTrigger value="context">Context</TabsTrigger>
                <TabsTrigger value="input">Input</TabsTrigger>
              </TabsList>

              {/* Task Execution Tab */}
              <TabsContent value="tasks" className="flex-1 overflow-auto">
                {taskInstances.length === 0 ? (
                  <Card>
                    <CardContent className="pt-6">
                      <p className="text-center text-muted-foreground">No task instances found</p>
                    </CardContent>
                  </Card>
                ) : (
                  <div className="space-y-4">
                    {taskInstances.map((task) => (
                      <Card key={task.id}>
                        <CardHeader>
                          <div className="flex items-center justify-between">
                            <div className="flex items-center gap-3">
                              {getTaskStatusIcon(task.state)}
                              <div>
                                <CardTitle className="text-base">{task.node_id}</CardTitle>
                                <CardDescription>
                                  {task.node_type} • Attempt {task.attempt}
                                </CardDescription>
                              </div>
                            </div>
                            <Badge variant={getTaskStatusBadgeVariant(task.state)}>
                              {task.state}
                            </Badge>
                          </div>
                        </CardHeader>
                        <CardContent className="space-y-4">
                          {/* Timestamps */}
                          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                            {task.scheduled_at && (
                              <div>
                                <p className="text-muted-foreground">Scheduled</p>
                                <p>{format(new Date(task.scheduled_at), 'PPpp')}</p>
                              </div>
                            )}
                            {task.started_at && (
                              <div>
                                <p className="text-muted-foreground">Started</p>
                                <p>{format(new Date(task.started_at), 'PPpp')}</p>
                              </div>
                            )}
                            {task.finished_at && (
                              <div>
                                <p className="text-muted-foreground">Finished</p>
                                <p>{format(new Date(task.finished_at), 'PPpp')}</p>
                              </div>
                            )}
                            {task.completed_at && (
                              <div>
                                <p className="text-muted-foreground">Completed</p>
                                <p>{format(new Date(task.completed_at), 'PPpp')}</p>
                              </div>
                            )}
                          </div>

                          {/* Error Display */}
                          {task.state === 'FAILED' && task.last_error && (
                            <div className="border border-destructive rounded-md p-4 bg-destructive/10">
                              <div className="flex items-center gap-2 mb-2">
                                <XCircle className="h-4 w-4 text-destructive" />
                                <h4 className="font-semibold text-destructive">Error</h4>
                              </div>
                              <pre className="text-sm overflow-auto whitespace-pre-wrap">
                                {formatError(task.last_error)}
                              </pre>
                            </div>
                          )}

                          {/* Input/Output */}
                          <div className="grid gap-4 md:grid-cols-2">
                            {task.input && (
                              <Collapsible>
                                <CollapsibleTrigger className="flex items-center justify-between w-full text-left">
                                  <span className="font-medium text-sm">Input</span>
                                  <ChevronDown className="h-4 w-4" />
                                </CollapsibleTrigger>
                                <CollapsibleContent className="mt-2">
                                  <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
                                    <Editor
                                      height="200px"
                                      defaultLanguage="json"
                                      value={JSON.stringify(task.input, null, 2)}
                                      options={{
                                        readOnly: true,
                                        minimap: { enabled: false },
                                        fontSize: 12,
                                        lineNumbers: "on",
                                        wordWrap: "on",
                                        scrollBeyondLastLine: false,
                                      }}
                                      theme="vs-dark"
                                    />
                                  </div>
                                </CollapsibleContent>
                              </Collapsible>
                            )}

                            {task.output && (
                              <Collapsible>
                                <CollapsibleTrigger className="flex items-center justify-between w-full text-left">
                                  <span className="font-medium text-sm">Output</span>
                                  <ChevronDown className="h-4 w-4" />
                                </CollapsibleTrigger>
                                <CollapsibleContent className="mt-2">
                                  <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
                                    <Editor
                                      height="200px"
                                      defaultLanguage="json"
                                      value={JSON.stringify(task.output, null, 2)}
                                      options={{
                                        readOnly: true,
                                        minimap: { enabled: false },
                                        fontSize: 12,
                                        lineNumbers: "on",
                                        wordWrap: "on",
                                        scrollBeyondLastLine: false,
                                      }}
                                      theme="vs-dark"
                                    />
                                  </div>
                                </CollapsibleContent>
                              </Collapsible>
                            )}
                          </div>
                        </CardContent>
                      </Card>
                    ))}
                  </div>
                )}
              </TabsContent>

              {/* Context Tab */}
              <TabsContent value="context" className="flex-1 overflow-auto">
                <Card>
                  <CardHeader>
                    <CardTitle>Workflow Context</CardTitle>
                    <CardDescription>
                      Runtime context including input, config, and task results
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    {context ? (
                      <div className="space-y-4">
                        {context.input && (
                          <div>
                            <h4 className="font-medium mb-2">Input</h4>
                            <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
                              <Editor
                                height="200px"
                                defaultLanguage="json"
                                value={JSON.stringify(context.input, null, 2)}
                                options={{
                                  readOnly: true,
                                  minimap: { enabled: false },
                                  fontSize: 12,
                                  lineNumbers: "on",
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
                            <h4 className="font-medium mb-2">Config</h4>
                            <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
                              <Editor
                                height="200px"
                                defaultLanguage="json"
                                value={JSON.stringify(context.config, null, 2)}
                                options={{
                                  readOnly: true,
                                  minimap: { enabled: false },
                                  fontSize: 12,
                                  lineNumbers: "on",
                                  wordWrap: "on",
                                  scrollBeyondLastLine: false,
                                }}
                                theme="vs-dark"
                              />
                            </div>
                          </div>
                        )}
                        {context.tasks && Object.keys(context.tasks).length > 0 && (
                          <div>
                            <h4 className="font-medium mb-2">Task Results</h4>
                            <div className="border rounded-md overflow-hidden" style={{ height: '300px' }}>
                              <Editor
                                height="300px"
                                defaultLanguage="json"
                                value={JSON.stringify(context.tasks, null, 2)}
                                options={{
                                  readOnly: true,
                                  minimap: { enabled: false },
                                  fontSize: 12,
                                  lineNumbers: "on",
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
                      <p className="text-muted-foreground">No context data available</p>
                    )}
                  </CardContent>
                </Card>
              </TabsContent>

              {/* Input Tab */}
              <TabsContent value="input" className="flex-1 overflow-auto">
                <Card>
                  <CardHeader>
                    <CardTitle>Execution Input</CardTitle>
                    <CardDescription>Input data passed to the workflow execution</CardDescription>
                  </CardHeader>
                  <CardContent>
                    {execution.input ? (
                      <div className="border rounded-md overflow-hidden" style={{ height: '400px' }}>
                        <Editor
                          height="400px"
                          defaultLanguage="json"
                          value={
                            typeof execution.input === 'string'
                              ? execution.input
                              : JSON.stringify(execution.input, null, 2)
                          }
                          options={{
                            readOnly: true,
                            minimap: { enabled: false },
                            fontSize: 14,
                            lineNumbers: "on",
                            wordWrap: "on",
                            scrollBeyondLastLine: false,
                          }}
                          theme="vs-dark"
                        />
                      </div>
                    ) : (
                      <p className="text-muted-foreground">No input data</p>
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
