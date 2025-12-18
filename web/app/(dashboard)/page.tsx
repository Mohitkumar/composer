'use client';

import { useQuery } from '@tanstack/react-query';
import { workflowApi, executionApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Plus, Workflow as WorkflowIcon, Play, CheckCircle2, XCircle, Clock, TrendingUp } from 'lucide-react';
import Link from 'next/link';
import { format, subDays } from 'date-fns';
import { Badge } from '@/components/ui/badge';

export default function HomePage() {
  const { data: workflowResponse, isLoading: workflowsLoading, error: workflowsError, isError: workflowsIsError } = useQuery({
    queryKey: ['workflows', 'home'],
    queryFn: () => workflowApi.list(undefined, undefined, 1, 1000), // Get first 1000 workflows for home page
    retry: 2,
  });
  
  const workflows = workflowResponse?.workflows || [];

  const { data: executions, isLoading: executionsLoading, error: executionsError } = useQuery({
    queryKey: ['executions'],
    queryFn: () => executionApi.list({ limit: 1000 }),
    retry: 2,
  });

  // Log execution errors without failing the page
  if (executionsError) {
    console.error('Failed to load executions:', executionsError);
  }

  const isLoading = workflowsLoading || executionsLoading;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading...</p>
        </div>
      </div>
    );
  }

  if (workflowsIsError) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">
            Overview of your workflows and executions
          </p>
        </div>
        <Card>
          <CardContent className="pt-6">
            <div className="text-center py-12">
              <p className="text-destructive mb-2">Failed to load data</p>
              <p className="text-sm text-muted-foreground">
                {workflowsError instanceof Error ? workflowsError.message : 'Unknown error occurred'}
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }

  // Backend already returns only latest version of each workflow, so no need to filter duplicates
  const recentWorkflows = workflows.slice(0, 5);
  const enabledCount = workflows.filter((w) => w.enabled).length || 0;
  const totalCount = workflows.length;

  // Filter out duplicates by ID and calculate execution metrics
  const allExecutions = executions?.filter((execution, index, self) => 
    index === self.findIndex((e) => e.id === execution.id)
  ) || [];
  const runningExecutions = allExecutions.filter((e) => e.status === 'RUNNING').length;
  const completedExecutions = allExecutions.filter(
    (e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED'
  ).length;
  const failedExecutions = allExecutions.filter((e) => e.status === 'FAILED').length;
  const totalExecutions = allExecutions.length;
  const successRate = totalExecutions > 0 ? Math.round((completedExecutions / totalExecutions) * 100) : 0;

  // Get failed executions from last 24 hours
  const last24Hours = subDays(new Date(), 1);
  const failedLast24Hours = allExecutions.filter((e) => {
    if (e.status !== 'FAILED') return false;
    try {
      const createdAt = new Date(e.created_at);
      return !isNaN(createdAt.getTime()) && createdAt >= last24Hours;
    } catch {
      return false;
    }
  }).length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">
            Overview of your workflows and executions
          </p>
        </div>
        <Link href="/workflows/new">
          <Button>
            <Plus className="mr-2 h-4 w-4" />
            Create Workflow
          </Button>
        </Link>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Workflows</CardTitle>
            <WorkflowIcon className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{totalCount}</div>
            <p className="text-xs text-muted-foreground">
              {enabledCount} enabled
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Active Executions</CardTitle>
            <Play className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{runningExecutions}</div>
            <p className="text-xs text-muted-foreground">
              Currently running
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
            <CheckCircle2 className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{successRate}%</div>
            <p className="text-xs text-muted-foreground">
              {completedExecutions} of {totalExecutions} succeeded
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Failed Executions</CardTitle>
            <XCircle className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{failedLast24Hours}</div>
            <p className="text-xs text-muted-foreground">
              Last 24 hours ({failedExecutions} total)
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Execution Status Distribution</CardTitle>
            <CardDescription>Breakdown of execution statuses</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <CheckCircle2 className="h-4 w-4 text-green-500" />
                  <span>Completed</span>
                </div>
                <span className="font-semibold">{completedExecutions}</span>
              </div>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <Play className="h-4 w-4 text-blue-500" />
                  <span>Running</span>
                </div>
                <span className="font-semibold">{runningExecutions}</span>
              </div>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <XCircle className="h-4 w-4 text-red-500" />
                  <span>Failed</span>
                </div>
                <span className="font-semibold">{failedExecutions}</span>
              </div>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <Clock className="h-4 w-4 text-yellow-500" />
                  <span>Pending</span>
                </div>
                <span className="font-semibold">
                  {allExecutions.filter((e) => e.status === 'PENDING').length}
                </span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent Executions</CardTitle>
            <CardDescription>Latest workflow executions</CardDescription>
          </CardHeader>
          <CardContent>
            {allExecutions.length === 0 ? (
              <p className="text-sm text-muted-foreground">No executions yet</p>
            ) : (
              <div className="space-y-2">
                {allExecutions.slice(0, 10).map((execution, index) => (
                  <Link
                    key={`${execution.id}-${index}`}
                    href={`/executions/${execution.id}`}
                    className="block"
                  >
                    <div className="flex items-center justify-between p-2 border rounded hover:bg-accent/50 transition-colors">
                      <div>
                        <p className="text-sm font-medium">{execution.id.slice(0, 8)}...</p>
                        <p className="text-xs text-muted-foreground">
                          {execution.created_at
                            ? format(new Date(execution.created_at), 'MMM d, HH:mm')
                            : 'N/A'}
                        </p>
                      </div>
                      <Badge
                        variant={
                          execution.status === 'COMPLETED' || execution.status === 'SUCCEEDED'
                            ? 'default'
                            : execution.status === 'FAILED'
                            ? 'destructive'
                            : execution.status === 'RUNNING'
                            ? 'secondary'
                            : 'outline'
                        }
                      >
                        {execution.status}
                      </Badge>
                    </div>
                  </Link>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recent Workflows</CardTitle>
          <CardDescription>
            Your most recently created or updated workflows
          </CardDescription>
        </CardHeader>
        <CardContent>
          {recentWorkflows.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground">
              No workflows yet. Create your first workflow to get started.
            </div>
          ) : (
            <div className="space-y-4">
              {recentWorkflows.map((workflow, index) => (
                <div
                  key={`${workflow.id}-${index}`}
                  className="flex items-center justify-between p-4 border rounded-lg hover:bg-accent/50 transition-colors"
                >
                  <div className="flex-1">
                    <div className="flex items-center gap-2">
                      <h3 className="font-semibold">{workflow.name}</h3>
                      <Badge variant={workflow.enabled ? 'default' : 'secondary'}>
                        {workflow.enabled ? 'Enabled' : 'Disabled'}
                      </Badge>
                    </div>
                    <p className="text-sm text-muted-foreground mt-1">
                      ID: {workflow.id} • Version {workflow.version} • Created{' '}
                      {workflow.created_at
                        ? format(new Date(workflow.created_at), 'MMM d, yyyy')
                        : 'N/A'}
                    </p>
                  </div>
                  <Link href={`/workflows/${workflow.id}`}>
                    <Button variant="outline" size="sm">
                      View
                    </Button>
                  </Link>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
