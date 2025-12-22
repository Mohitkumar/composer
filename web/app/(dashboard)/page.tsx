'use client';

import { useQuery } from '@tanstack/react-query';
import { workflowApi, executionApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { CheckCircle2, XCircle, Clock, TrendingUp, BarChart3, Activity, Zap, AlertCircle } from 'lucide-react';
import { format, subDays, startOfDay, endOfDay, eachDayOfInterval } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { useSearchParams, useRouter } from 'next/navigation';
import { useState, useEffect, useMemo } from 'react';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  LineChart,
  Line,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';

export default function DashboardPage() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const workflowIdFromUrl = searchParams.get('workflow_id');
  const [selectedWorkflowId, setSelectedWorkflowId] = useState<string>(workflowIdFromUrl || 'all');
  const [dateRange, setDateRange] = useState<'7d' | '30d' | '90d' | 'all'>('30d');

  // Fetch workflows for filter dropdown
  const { data: workflowResponse } = useQuery({
    queryKey: ['workflows', 'dashboard'],
    queryFn: () => workflowApi.list(undefined, undefined, 1, 1000),
    retry: 2,
  });

  const workflows = workflowResponse?.workflows || [];

  // Calculate date range
  const dateFilter = useMemo(() => {
    if (dateRange === 'all') return undefined;
    const days = dateRange === '7d' ? 7 : dateRange === '30d' ? 30 : 90;
    return subDays(new Date(), days).toISOString();
  }, [dateRange]);

  // Fetch executions with filters
  const { data: executionResponse, isLoading: executionsLoading } = useQuery({
    queryKey: ['executions', 'dashboard', selectedWorkflowId, dateRange],
    queryFn: () =>
      executionApi.list({
        workflow_id: selectedWorkflowId !== 'all' ? selectedWorkflowId : undefined,
        page_size: 10000, // Get all for reporting
      }),
    retry: 2,
  });

  const executions = executionResponse?.executions || [];

  // Filter executions by date range
  const filteredExecutions = useMemo(() => {
    if (!dateFilter) return executions;
    const filterDate = new Date(dateFilter);
    return executions.filter((exec) => {
      try {
        const createdAt = new Date(exec.created_at);
        return createdAt >= filterDate;
      } catch {
        return false;
      }
    });
  }, [executions, dateFilter]);

  // Calculate metrics
  const metrics = useMemo(() => {
    const total = filteredExecutions.length;
    const executed = filteredExecutions.filter(
      (e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED'
    ).length;
    const failed = filteredExecutions.filter((e) => e.status === 'FAILED').length;
    const running = filteredExecutions.filter((e) => e.status === 'RUNNING').length;
    const pending = filteredExecutions.filter((e) => e.status === 'PENDING').length;

    // Calculate average time taken for completed executions
    const completedWithDuration = filteredExecutions.filter((e) => {
      return (e.status === 'COMPLETED' || e.status === 'SUCCEEDED') && e.completed_at && e.created_at;
    });
    
    const durations = completedWithDuration.map((e) => {
      const start = new Date(e.created_at).getTime();
      const end = new Date(e.completed_at!).getTime();
      return (end - start) / 1000; // Convert to seconds
    });

    const avgTimeSeconds = durations.length > 0
      ? durations.reduce((sum, d) => sum + d, 0) / durations.length
      : 0;

    const avgTimeMinutes = avgTimeSeconds / 60;
    const avgTimeDisplay = avgTimeMinutes >= 1
      ? `${Math.round(avgTimeMinutes * 10) / 10}m`
      : `${Math.round(avgTimeSeconds * 10) / 10}s`;

    return {
      total,
      executed,
      failed,
      running,
      pending,
      avgTimeSeconds,
      avgTimeDisplay,
      durations,
    };
  }, [filteredExecutions]);

  // Prepare data for execution trend chart (daily)
  const executionTrendData = useMemo(() => {
    const days = dateRange === '7d' ? 7 : dateRange === '30d' ? 30 : dateRange === '90d' ? 90 : 90;
    const startDate = subDays(new Date(), days);
    const daysList = eachDayOfInterval({ start: startDate, end: new Date() });

    return daysList.map((day) => {
      const dayStart = startOfDay(day);
      const dayEnd = endOfDay(day);
      const dayExecutions = filteredExecutions.filter((e) => {
        try {
          const createdAt = new Date(e.created_at);
          return createdAt >= dayStart && createdAt <= dayEnd;
        } catch {
          return false;
        }
      });

      return {
        date: format(day, 'MMM d'),
        executed: dayExecutions.filter((e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED').length,
        failed: dayExecutions.filter((e) => e.status === 'FAILED').length,
        pending: dayExecutions.filter((e) => e.status === 'PENDING').length,
        running: dayExecutions.filter((e) => e.status === 'RUNNING').length,
        total: dayExecutions.length,
      };
    });
  }, [filteredExecutions, dateRange]);

  // Prepare data for time taken trend
  const timeTakenTrendData = useMemo(() => {
    const days = dateRange === '7d' ? 7 : dateRange === '30d' ? 30 : dateRange === '90d' ? 90 : 90;
    const startDate = subDays(new Date(), days);
    const daysList = eachDayOfInterval({ start: startDate, end: new Date() });

    return daysList.map((day) => {
      const dayStart = startOfDay(day);
      const dayEnd = endOfDay(day);
      const dayExecutions = filteredExecutions.filter((e) => {
        try {
          const createdAt = new Date(e.created_at);
          return createdAt >= dayStart && createdAt <= dayEnd;
        } catch {
          return false;
        }
      });

      const completedExecutions = dayExecutions.filter(
        (e) => (e.status === 'COMPLETED' || e.status === 'SUCCEEDED') && e.completed_at && e.created_at
      );

      const dayDurations = completedExecutions.map((e) => {
        const start = new Date(e.created_at).getTime();
        const end = new Date(e.completed_at!).getTime();
        return (end - start) / 1000; // seconds
      });

      const avgTime = dayDurations.length > 0
        ? dayDurations.reduce((sum, d) => sum + d, 0) / dayDurations.length
        : 0;

      return {
        date: format(day, 'MMM d'),
        avgTime: Math.round(avgTime * 10) / 10, // Round to 1 decimal (seconds)
        count: completedExecutions.length,
      };
    });
  }, [filteredExecutions, dateRange]);

  // Error analysis - get failed executions with error details
  const errorAnalysis = useMemo(() => {
    const failedExecutions = filteredExecutions.filter((e) => e.status === 'FAILED');
    
    // Group by workflow if showing all workflows
    if (selectedWorkflowId === 'all') {
      const byWorkflow = new Map<string, number>();
      failedExecutions.forEach((exec) => {
        const count = byWorkflow.get(exec.workflow_definition_id) || 0;
        byWorkflow.set(exec.workflow_definition_id, count + 1);
      });

      return {
        totalFailed: failedExecutions.length,
        byWorkflow: Array.from(byWorkflow.entries())
          .map(([id, count]) => ({
            workflowId: id,
            workflowName: workflows.find((w) => w.id === id)?.name || id,
            count,
          }))
          .sort((a, b) => b.count - a.count)
          .slice(0, 10),
        recentFailures: failedExecutions
          .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
          .slice(0, 10),
      };
    }

    return {
      totalFailed: failedExecutions.length,
      byWorkflow: [],
      recentFailures: failedExecutions
        .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
        .slice(0, 10),
    };
  }, [filteredExecutions, selectedWorkflowId, workflows]);

  // Update URL when filter changes
  useEffect(() => {
    if (selectedWorkflowId && selectedWorkflowId !== 'all') {
      router.replace(`/?workflow_id=${selectedWorkflowId}`, { scroll: false });
    } else {
      router.replace('/', { scroll: false });
    }
  }, [selectedWorkflowId, router]);

  // Sync state with URL on mount
  useEffect(() => {
    if (workflowIdFromUrl && workflowIdFromUrl !== selectedWorkflowId) {
      setSelectedWorkflowId(workflowIdFromUrl);
    }
  }, [workflowIdFromUrl]);

  const isLoading = executionsLoading;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading dashboard...</p>
        </div>
      </div>
    );
  }

  const selectedWorkflow = workflows.find((w) => w.id === selectedWorkflowId);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Dashboard</h1>
          <p className="text-muted-foreground">
            Comprehensive reporting and analytics for workflow executions
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={dateRange} onValueChange={(value: '7d' | '30d' | '90d' | 'all') => setDateRange(value)}>
            <SelectTrigger className="w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="7d">Last 7 days</SelectItem>
              <SelectItem value="30d">Last 30 days</SelectItem>
              <SelectItem value="90d">Last 90 days</SelectItem>
              <SelectItem value="all">All time</SelectItem>
            </SelectContent>
          </Select>
          <Select value={selectedWorkflowId} onValueChange={setSelectedWorkflowId}>
            <SelectTrigger className="w-[250px]">
              <SelectValue placeholder="All Workflows" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All Workflows</SelectItem>
              {workflows.map((workflow) => (
                <SelectItem key={workflow.id} value={workflow.id}>
                  {workflow.name} ({workflow.id})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      {selectedWorkflowId !== 'all' && selectedWorkflow && (
        <Card>
          <CardContent className="pt-4">
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">Filtered by:</span>
              <Badge variant="secondary">{selectedWorkflow.name}</Badge>
              <Badge variant="outline" className="font-mono text-xs">
                {selectedWorkflowId}
              </Badge>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Metrics */}
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-5">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Executed</CardTitle>
            <CheckCircle2 className="h-4 w-4 text-green-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.executed}</div>
            <p className="text-xs text-muted-foreground">
              {metrics.total > 0 ? Math.round((metrics.executed / metrics.total) * 100) : 0}% of total
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Failed</CardTitle>
            <XCircle className="h-4 w-4 text-red-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.failed}</div>
            <p className="text-xs text-muted-foreground">
              {metrics.total > 0 ? Math.round((metrics.failed / metrics.total) * 100) : 0}% of total
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Pending</CardTitle>
            <Clock className="h-4 w-4 text-yellow-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.pending}</div>
            <p className="text-xs text-muted-foreground">
              {metrics.total > 0 ? Math.round((metrics.pending / metrics.total) * 100) : 0}% of total
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Running</CardTitle>
            <Zap className="h-4 w-4 text-blue-500" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.running}</div>
            <p className="text-xs text-muted-foreground">
              {metrics.total > 0 ? Math.round((metrics.running / metrics.total) * 100) : 0}% of total
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Avg Time Taken</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.avgTimeDisplay}</div>
            <p className="text-xs text-muted-foreground">
              Average execution duration
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Graphs */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Execution Trend</CardTitle>
            <CardDescription>Daily execution volume by status</CardDescription>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={350}>
              <LineChart data={executionTrendData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="date" />
                <YAxis />
                <Tooltip />
                <Legend />
                <Line type="monotone" dataKey="executed" stroke="#10b981" name="Executed" strokeWidth={2} />
                <Line type="monotone" dataKey="failed" stroke="#ef4444" name="Failed" strokeWidth={2} />
                <Line type="monotone" dataKey="pending" stroke="#f59e0b" name="Pending" strokeWidth={2} />
                <Line type="monotone" dataKey="running" stroke="#3b82f6" name="Running" strokeWidth={2} />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Latency</CardTitle>
            <CardDescription>Latency per day</CardDescription>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={350}>
              <LineChart data={timeTakenTrendData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="date" />
                <YAxis label={{ value: 'Time (seconds)', angle: -90, position: 'insideLeft' }} />
                <Tooltip formatter={(value) => `${value} seconds`} />
                <Legend />
                <Line
                  type="monotone"
                  dataKey="avgTime"
                  stroke="#8b5cf6"
                  name="Avg Time (seconds)"
                  strokeWidth={2}
                  dot={{ r: 4 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>

      {/* Error Analysis */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <AlertCircle className="h-5 w-5 text-destructive" />
            Error Analysis
          </CardTitle>
          <CardDescription>
            Analysis of failed executions and error patterns
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="space-y-6">
            <div>
              <h3 className="text-lg font-semibold mb-4">Summary</h3>
              <div className="grid gap-4 md:grid-cols-3">
                <div className="border rounded-lg p-4">
                  <div className="text-2xl font-bold text-destructive">{errorAnalysis.totalFailed}</div>
                  <div className="text-sm text-muted-foreground">Total Failed Executions</div>
                </div>
                <div className="border rounded-lg p-4">
                  <div className="text-2xl font-bold">
                    {metrics.total > 0 ? Math.round((errorAnalysis.totalFailed / metrics.total) * 100) : 0}%
                  </div>
                  <div className="text-sm text-muted-foreground">Failure Rate</div>
                </div>
                <div className="border rounded-lg p-4">
                  <div className="text-2xl font-bold">
                    {errorAnalysis.totalFailed > 0 && metrics.executed > 0
                      ? Math.round((errorAnalysis.totalFailed / (errorAnalysis.totalFailed + metrics.executed)) * 100)
                      : 0}%
                  </div>
                  <div className="text-sm text-muted-foreground">Failure vs Success Ratio</div>
                </div>
              </div>
            </div>

            {selectedWorkflowId === 'all' && errorAnalysis.byWorkflow.length > 0 && (
              <div>
                <h3 className="text-lg font-semibold mb-4">Failures by Workflow</h3>
                <div className="border rounded-lg overflow-hidden">
                  <div className="max-h-[300px] overflow-y-auto">
                    <table className="w-full">
                      <thead className="bg-muted/50 sticky top-0">
                        <tr>
                          <th className="text-left p-3 text-sm font-medium">Workflow</th>
                          <th className="text-left p-3 text-sm font-medium">Workflow ID</th>
                          <th className="text-right p-3 text-sm font-medium">Failed Count</th>
                        </tr>
                      </thead>
                      <tbody>
                        {errorAnalysis.byWorkflow.map((item) => (
                          <tr key={item.workflowId} className="border-t">
                            <td className="p-3 text-sm">{item.workflowName}</td>
                            <td className="p-3 text-sm font-mono text-xs">{item.workflowId}</td>
                            <td className="p-3 text-sm text-right font-semibold text-destructive">
                              {item.count}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              </div>
            )}

            {errorAnalysis.recentFailures.length > 0 && (
              <div>
                <h3 className="text-lg font-semibold mb-4">Recent Failures</h3>
                <div className="border rounded-lg overflow-hidden">
                  <div className="max-h-[400px] overflow-y-auto">
                    <table className="w-full">
                      <thead className="bg-muted/50 sticky top-0">
                        <tr>
                          <th className="text-left p-3 text-sm font-medium">Execution ID</th>
                          <th className="text-left p-3 text-sm font-medium">Workflow ID</th>
                          <th className="text-left p-3 text-sm font-medium">Failed At</th>
                          <th className="text-right p-3 text-sm font-medium">Duration</th>
                        </tr>
                      </thead>
                      <tbody>
                        {errorAnalysis.recentFailures.map((execution) => {
                          const duration = execution.completed_at && execution.created_at
                            ? (() => {
                                const start = new Date(execution.created_at).getTime();
                                const end = new Date(execution.completed_at).getTime();
                                const minutes = (end - start) / 1000 / 60;
                                return minutes >= 60 ? `${Math.round(minutes / 60 * 10) / 10}h` : `${Math.round(minutes)}m`;
                              })()
                            : '-';

                          return (
                            <tr key={execution.id} className="border-t hover:bg-muted/30">
                              <td className="p-3 text-sm font-mono text-xs">{execution.id.slice(0, 8)}...</td>
                              <td className="p-3 text-sm font-mono text-xs">{execution.workflow_definition_id}</td>
                              <td className="p-3 text-sm text-muted-foreground">
                                {execution.completed_at
                                  ? format(new Date(execution.completed_at), 'MMM d, yyyy HH:mm')
                                  : execution.created_at
                                  ? format(new Date(execution.created_at), 'MMM d, yyyy HH:mm')
                                  : '-'}
                              </td>
                              <td className="p-3 text-sm text-right">{duration}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </div>
              </div>
            )}

            {errorAnalysis.totalFailed === 0 && (
              <div className="text-center py-8 text-muted-foreground">
                <CheckCircle2 className="h-12 w-12 mx-auto mb-2 text-green-500" />
                <p>No failed executions in the selected time period</p>
              </div>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
