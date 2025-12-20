'use client';

import { useQuery } from '@tanstack/react-query';
import { workflowApi, executionApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { CheckCircle2, XCircle, Clock, TrendingUp, BarChart3, Activity, Zap } from 'lucide-react';
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
  PieChart,
  Pie,
  Cell,
} from 'recharts';

const COLORS = ['#10b981', '#ef4444', '#3b82f6', '#f59e0b'];

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
    const completed = filteredExecutions.filter(
      (e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED'
    ).length;
    const failed = filteredExecutions.filter((e) => e.status === 'FAILED').length;
    const running = filteredExecutions.filter((e) => e.status === 'RUNNING').length;
    const pending = filteredExecutions.filter((e) => e.status === 'PENDING').length;
    const successRate = total > 0 ? Math.round((completed / total) * 100) : 0;

    // Calculate average duration
    const completedWithDuration = filteredExecutions.filter((e) => {
      return (e.status === 'COMPLETED' || e.status === 'SUCCEEDED') && e.completed_at && e.created_at;
    });
    const avgDuration = completedWithDuration.length > 0
      ? completedWithDuration.reduce((sum, e) => {
          const start = new Date(e.created_at).getTime();
          const end = new Date(e.completed_at!).getTime();
          return sum + (end - start);
        }, 0) / completedWithDuration.length / 1000 / 60 // Convert to minutes
      : 0;

    return {
      total,
      completed,
      failed,
      running,
      pending,
      successRate,
      avgDuration: Math.round(avgDuration * 10) / 10,
    };
  }, [filteredExecutions]);

  // Prepare data for execution trends chart (daily)
  const executionTrendsData = useMemo(() => {
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

      const dayCompleted = dayExecutions.filter(
        (e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED'
      ).length;
      const dayFailed = dayExecutions.filter((e) => e.status === 'FAILED').length;

      return {
        date: format(day, 'MMM d'),
        total: dayExecutions.length,
        completed: dayCompleted,
        failed: dayFailed,
      };
    });
  }, [filteredExecutions, dateRange]);

  // Prepare data for success rate over time
  const successRateData = useMemo(() => {
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

      const dayCompleted = dayExecutions.filter(
        (e) => e.status === 'COMPLETED' || e.status === 'SUCCEEDED'
      ).length;
      const successRate = dayExecutions.length > 0
        ? Math.round((dayCompleted / dayExecutions.length) * 100)
        : 0;

      return {
        date: format(day, 'MMM d'),
        successRate,
      };
    });
  }, [filteredExecutions, dateRange]);

  // Status distribution for pie chart
  const statusDistributionData = useMemo(() => {
    return [
      { name: 'Completed', value: metrics.completed, color: COLORS[0] },
      { name: 'Failed', value: metrics.failed, color: COLORS[1] },
      { name: 'Running', value: metrics.running, color: COLORS[2] },
      { name: 'Pending', value: metrics.pending, color: COLORS[3] },
    ].filter((item) => item.value > 0);
  }, [metrics]);

  // Execution by workflow (if no specific workflow selected)
  const executionsByWorkflow = useMemo(() => {
    if (selectedWorkflowId !== 'all') return [];
    const workflowMap = new Map<string, number>();
    filteredExecutions.forEach((exec) => {
      const count = workflowMap.get(exec.workflow_definition_id) || 0;
      workflowMap.set(exec.workflow_definition_id, count + 1);
    });
    return Array.from(workflowMap.entries())
      .map(([id, count]) => ({
        workflowId: id,
        count,
      }))
      .sort((a, b) => b.count - a.count)
      .slice(0, 10);
  }, [filteredExecutions, selectedWorkflowId]);

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

      {/* Key Metrics */}
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Executions</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.total}</div>
            <p className="text-xs text-muted-foreground">
              {dateRange === 'all' ? 'All time' : `Last ${dateRange}`}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
            <TrendingUp className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.successRate}%</div>
            <p className="text-xs text-muted-foreground">
              {metrics.completed} of {metrics.total} succeeded
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Failed Executions</CardTitle>
            <XCircle className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.failed}</div>
            <p className="text-xs text-muted-foreground">
              {metrics.total > 0
                ? `${Math.round((metrics.failed / metrics.total) * 100)}% failure rate`
                : 'No executions'}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Avg Duration</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{metrics.avgDuration}m</div>
            <p className="text-xs text-muted-foreground">
              Average execution time
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Charts Row 1 */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Execution Trends</CardTitle>
            <CardDescription>Daily execution volume over time</CardDescription>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={executionTrendsData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="date" />
                <YAxis />
                <Tooltip />
                <Legend />
                <Line type="monotone" dataKey="total" stroke="#3b82f6" name="Total" />
                <Line type="monotone" dataKey="completed" stroke="#10b981" name="Completed" />
                <Line type="monotone" dataKey="failed" stroke="#ef4444" name="Failed" />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Success Rate Trend</CardTitle>
            <CardDescription>Daily success rate percentage</CardDescription>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={successRateData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="date" />
                <YAxis domain={[0, 100]} />
                <Tooltip formatter={(value) => `${value}%`} />
                <Line
                  type="monotone"
                  dataKey="successRate"
                  stroke="#10b981"
                  name="Success Rate (%)"
                  strokeWidth={2}
                />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>

      {/* Charts Row 2 */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Status Distribution</CardTitle>
            <CardDescription>Breakdown of execution statuses</CardDescription>
          </CardHeader>
          <CardContent>
            {statusDistributionData.length > 0 ? (
              <ResponsiveContainer width="100%" height={300}>
                <PieChart>
                  <Pie
                    data={statusDistributionData}
                    cx="50%"
                    cy="50%"
                    labelLine={false}
                    label={({ name, percent }) => `${name}: ${(percent * 100).toFixed(0)}%`}
                    outerRadius={80}
                    fill="#8884d8"
                    dataKey="value"
                  >
                    {statusDistributionData.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.color} />
                    ))}
                  </Pie>
                  <Tooltip />
                </PieChart>
              </ResponsiveContainer>
            ) : (
              <div className="flex items-center justify-center h-[300px] text-muted-foreground">
                No data available
              </div>
            )}
          </CardContent>
        </Card>

        {selectedWorkflowId === 'all' && executionsByWorkflow.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle>Top Workflows by Execution Count</CardTitle>
              <CardDescription>Most executed workflows</CardDescription>
            </CardHeader>
            <CardContent>
              <ResponsiveContainer width="100%" height={300}>
                <BarChart data={executionsByWorkflow}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis
                    dataKey="workflowId"
                    angle={-45}
                    textAnchor="end"
                    height={100}
                    tick={{ fontSize: 10 }}
                  />
                  <YAxis />
                  <Tooltip />
                  <Bar dataKey="count" fill="#3b82f6" name="Executions" />
                </BarChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>
        )}

        {selectedWorkflowId !== 'all' && (
          <Card>
            <CardHeader>
              <CardTitle>Execution Status Breakdown</CardTitle>
              <CardDescription>Detailed status distribution</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <CheckCircle2 className="h-4 w-4 text-green-500" />
                    <span>Completed</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-semibold">{metrics.completed}</span>
                    <span className="text-xs text-muted-foreground">
                      ({metrics.total > 0 ? Math.round((metrics.completed / metrics.total) * 100) : 0}%)
                    </span>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <XCircle className="h-4 w-4 text-red-500" />
                    <span>Failed</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-semibold">{metrics.failed}</span>
                    <span className="text-xs text-muted-foreground">
                      ({metrics.total > 0 ? Math.round((metrics.failed / metrics.total) * 100) : 0}%)
                    </span>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Zap className="h-4 w-4 text-blue-500" />
                    <span>Running</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-semibold">{metrics.running}</span>
                    <span className="text-xs text-muted-foreground">
                      ({metrics.total > 0 ? Math.round((metrics.running / metrics.total) * 100) : 0}%)
                    </span>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Clock className="h-4 w-4 text-yellow-500" />
                    <span>Pending</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="font-semibold">{metrics.pending}</span>
                    <span className="text-xs text-muted-foreground">
                      ({metrics.total > 0 ? Math.round((metrics.pending / metrics.total) * 100) : 0}%)
                    </span>
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      {/* Additional Metrics */}
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Execution Velocity</CardTitle>
            <CardDescription>Executions per day</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="text-3xl font-bold">
              {dateRange === 'all'
                ? metrics.total > 0 && filteredExecutions.length > 0
                  ? (() => {
                      const oldestExecution = filteredExecutions.reduce((oldest, exec) => {
                        const execDate = new Date(exec.created_at).getTime();
                        const oldestDate = new Date(oldest.created_at).getTime();
                        return execDate < oldestDate ? exec : oldest;
                      }, filteredExecutions[0]);
                      const daysSinceFirst = Math.max(
                        1,
                        Math.floor(
                          (new Date().getTime() - new Date(oldestExecution.created_at).getTime()) /
                            (1000 * 60 * 60 * 24)
                        )
                      );
                      return Math.round(metrics.total / daysSinceFirst);
                    })()
                  : 0
                : dateRange === '7d'
                ? Math.round(metrics.total / 7)
                : dateRange === '30d'
                ? Math.round(metrics.total / 30)
                : Math.round(metrics.total / 90)}
            </div>
            <p className="text-xs text-muted-foreground mt-2">Average daily executions</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Failure Trend</CardTitle>
            <CardDescription>Failed vs total ratio</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="text-3xl font-bold">
              {metrics.total > 0 ? Math.round((metrics.failed / metrics.total) * 100) : 0}%
            </div>
            <p className="text-xs text-muted-foreground mt-2">
              {metrics.failed} failures out of {metrics.total} total
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Completion Rate</CardTitle>
            <CardDescription>Successfully completed executions</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="text-3xl font-bold">{metrics.completed}</div>
            <p className="text-xs text-muted-foreground mt-2">
              {metrics.total > 0 ? Math.round((metrics.completed / metrics.total) * 100) : 0}% of all executions
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
