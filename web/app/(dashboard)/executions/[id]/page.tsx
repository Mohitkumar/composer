'use client';

import { useParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';
import { executionApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { format } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ArrowLeft, X } from 'lucide-react';
import Link from 'next/link';
import { useMutation, useQueryClient } from '@tanstack/react-query';

export default function ExecutionDetailPage() {
  const params = useParams();
  const executionId = params.id as string;
  const queryClient = useQueryClient();

  const { data: execution, isLoading } = useQuery({
    queryKey: ['execution', executionId],
    queryFn: () => executionApi.get(executionId),
  });

  const cancelMutation = useMutation({
    mutationFn: () => executionApi.cancel(executionId, 'Cancelled by user'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['execution', executionId] });
      queryClient.invalidateQueries({ queryKey: ['executions'] });
    },
  });

  if (isLoading) {
    return <div>Loading execution details...</div>;
  }

  if (!execution) {
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

  const canCancel = execution.status === 'PENDING' || execution.status === 'RUNNING';

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

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Basic Information</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <p className="text-sm text-muted-foreground">Execution ID</p>
              <p className="font-mono text-sm">{execution.id}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Workflow ID</p>
              <p className="font-mono text-sm">{execution.workflow_definition_id}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Version</p>
              <p>v{execution.workflow_version}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Status</p>
              <Badge variant={getStatusBadgeVariant(execution.status)}>
                {execution.status}
              </Badge>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Timestamps</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <p className="text-sm text-muted-foreground">Created At</p>
              <p>
                {execution.created_at
                  ? format(new Date(execution.created_at), 'PPpp')
                  : 'N/A'}
              </p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Updated At</p>
              <p>
                {execution.updated_at
                  ? format(new Date(execution.updated_at), 'PPpp')
                  : 'N/A'}
              </p>
            </div>
            {execution.completed_at && (
              <div>
                <p className="text-sm text-muted-foreground">Completed At</p>
                <p>
                  {typeof execution.completed_at === 'string'
                    ? format(new Date(execution.completed_at), 'PPpp')
                    : 'N/A'}
                </p>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {execution.input && (
        <Card>
          <CardHeader>
            <CardTitle>Input</CardTitle>
            <CardDescription>Execution input data</CardDescription>
          </CardHeader>
          <CardContent>
            <pre className="bg-muted p-4 rounded-md overflow-auto text-sm">
              {execution.input
                ? JSON.stringify(execution.input, null, 2)
                : 'No input data'}
            </pre>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
