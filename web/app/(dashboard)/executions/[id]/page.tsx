'use client';

import { useParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';
import { executionApi, type ExecutionDetail, type TaskInstance } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { format, formatDistance } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ArrowLeft, X } from 'lucide-react';
import Link from 'next/link';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { ChevronDown, CheckCircle2, XCircle, Clock, PlayCircle, AlertCircle, MinusCircle, Hourglass } from 'lucide-react';
import Editor from '@monaco-editor/react';

export default function ExecutionDetailPage() {
  const params = useParams();
  const executionId = params.id as string;
  const queryClient = useQueryClient();

  const { data: executionDetail, isLoading } = useQuery({
    queryKey: ['execution-detail', executionId],
    queryFn: () => executionApi.getDetails(executionId),
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
    },
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading execution details...</p>
        </div>
      </div>
    );
  }

  if (!executionDetail) {
    return (
      <div className="text-center py-12">
        <p className="text-muted-foreground">Execution not found</p>
        <Link href="/executions">
          <Button variant="outline" className="mt-4">
            <ArrowLeft className="mr-2 h-4 w-4" />
            Back to Executions
          </Button>
        </Link>
      </div>
    );
  }

  const execution = executionDetail.execution;
  const context = executionDetail.context;
  const taskInstances = executionDetail.task_instances || [];

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
      case 'SKIPPED':
        return <MinusCircle className="h-4 w-4 text-gray-400" />;
      case 'WAITING':
        return <Hourglass className="h-4 w-4 text-purple-500" />;
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

  const canCancel = execution.status === 'PENDING' || execution.status === 'RUNNING';

  const duration = execution.completed_at
    ? formatDistance(
        new Date(execution.completed_at),
        new Date(execution.created_at)
      )
    : execution.status === 'RUNNING'
    ? formatDistance(new Date(), new Date(execution.created_at))
    : null;

  const formatError = (error: any): string => {
    if (typeof error === 'string') return error;
    if (error?.message) return error.message;
    if (error?.error) return error.error;
    return JSON.stringify(error, null, 2);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <Link href="/executions">
            <Button variant="ghost" size="sm" className="mb-2">
              <ArrowLeft className="mr-2 h-4 w-4" />
              Back
            </Button>
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">Execution Details</h1>
          <p className="text-muted-foreground">Execution ID: {execution.id}</p>
        </div>
        {canCancel && (
          <Button
            variant="destructive"
            onClick={() => cancelMutation.mutate()}
            disabled={cancelMutation.isPending}
          >
            <X className="mr-2 h-4 w-4" />
            {cancelMutation.isPending ? 'Cancelling...' : 'Cancel Execution'}
          </Button>
        )}
      </div>

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
          >
            {execution.workflow_definition_id}
          </Link>
          <span className="text-muted-foreground">v{execution.workflow_version}</span>
        </div>
      </div>

      <Tabs defaultValue="tasks" className="space-y-4">
        <TabsList>
          <TabsTrigger value="tasks">Task Execution</TabsTrigger>
          <TabsTrigger value="context">Context</TabsTrigger>
        </TabsList>

        {/* Task Execution Tab */}
        <TabsContent value="tasks" className="space-y-4">
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
        <TabsContent value="context" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Workflow Context</CardTitle>
              <CardDescription>
                Runtime context including input, config, and task results
              </CardDescription>
            </CardHeader>
            <CardContent>
              {context ? (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {context.input && (
                    <div>
                      <h4 className="font-medium mb-2">Input</h4>
                      <div className="border rounded-md overflow-hidden" style={{ height: '300px' }}>
                        <Editor
                          height="300px"
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
                      <div className="border rounded-md overflow-hidden" style={{ height: '300px' }}>
                        <Editor
                          height="300px"
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
                </div>
              ) : (
                <p className="text-muted-foreground">No context data available</p>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
