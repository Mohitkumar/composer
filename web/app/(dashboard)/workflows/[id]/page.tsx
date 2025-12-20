'use client';

import { useParams, useRouter } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';
import { workflowApi } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ArrowLeft, Edit, Play, Trash2, Rocket, RotateCcw } from 'lucide-react';
import Link from 'next/link';
import { format } from 'date-fns';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

export default function WorkflowDetailPage() {
  const params = useParams();
  const router = useRouter();
  const workflowId = params.id as string;
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [rollbackDialogOpen, setRollbackDialogOpen] = useState(false);
  const [selectedRollbackVersion, setSelectedRollbackVersion] = useState<number | null>(null);
  const queryClient = useQueryClient();

  const { data: workflow, isLoading } = useQuery({
    queryKey: ['workflow', workflowId],
    queryFn: () => workflowApi.get(workflowId),
  });

  // Get previous deployed versions for rollback
  const { data: previousDeployedVersions = [] } = useQuery({
    queryKey: ['previous-deployed-versions', workflowId, workflow?.version],
    queryFn: () => workflowApi.getPreviousDeployedVersions(workflowId, workflow?.version || 0),
    enabled: !!workflow && workflow.deployed && workflow.version > 1,
  });

  const deleteMutation = useMutation({
    mutationFn: () => workflowApi.delete(workflowId, workflow?.version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      toast.success('Workflow deleted successfully');
      router.push('/workflows');
    },
    onError: (error) => {
      toast.error('Failed to delete workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const executeMutation = useMutation({
    mutationFn: () =>
      workflowApi.execute(workflowId, {
        workflow_version: 0, // 0 means use deployed version
        input: {},
      }),
    onSuccess: (data) => {
      router.push(`/executions/${data.run_id}`);
    },
  });

  const deployMutation = useMutation({
    mutationFn: () => workflowApi.deploy(workflowId, workflow?.version || 1),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      toast.success('Workflow deployed successfully');
    },
    onError: (error) => {
      toast.error('Failed to deploy workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const rollbackMutation = useMutation({
    mutationFn: (version: number) => workflowApi.rollback(workflowId, version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      toast.success('Workflow rolled back successfully');
      setRollbackDialogOpen(false);
      setSelectedRollbackVersion(null);
    },
    onError: (error) => {
      toast.error('Failed to rollback workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return <div>Loading workflow...</div>;
  }

  if (!workflow) {
    return (
      <div className="text-center py-12">
        <p className="text-muted-foreground">Workflow not found</p>
        <Link href="/workflows">
          <Button variant="outline" className="mt-4">
            <ArrowLeft className="mr-2 h-4 w-4" />
            Back to Workflows
          </Button>
        </Link>
      </div>
    );
  }

  // Parse the spec from JSON if it's a string
  let spec;
  if (typeof workflow.spec === 'string') {
    spec = JSON.parse(workflow.spec);
  } else {
    spec = workflow.spec;
  }

  const handleDelete = () => {
    deleteMutation.mutate();
  };

  const handleExecute = () => {
    executeMutation.mutate();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <Link href="/workflows">
            <Button variant="ghost" size="sm" className="mb-2">
              <ArrowLeft className="mr-2 h-4 w-4" />
              Back
            </Button>
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{workflow.name}</h1>
          <p className="text-muted-foreground">Workflow ID: {workflow.id}</p>
        </div>
        <div className="flex gap-2">
          <Button onClick={handleExecute} disabled={executeMutation.isPending}>
            <Play className="mr-2 h-4 w-4" />
            {executeMutation.isPending ? 'Executing...' : 'Execute'}
          </Button>
          {!workflow.deployed && (
            <Button
              onClick={() => deployMutation.mutate()}
              disabled={deployMutation.isPending}
              variant="default"
            >
              <Rocket className="mr-2 h-4 w-4" />
              {deployMutation.isPending ? 'Deploying...' : 'Deploy'}
            </Button>
          )}
          <Button
            onClick={() => setRollbackDialogOpen(true)}
            variant="outline"
            disabled={!workflow.deployed || (previousDeployedVersions?.length || 0) === 0}
          >
            <RotateCcw className="mr-2 h-4 w-4" />
            Rollback
          </Button>
          <Link href={`/workflows/${workflowId}/edit`}>
            <Button variant="outline">
              <Edit className="mr-2 h-4 w-4" />
              Edit
            </Button>
          </Link>
          <Button
            variant="destructive"
            onClick={() => setDeleteDialogOpen(true)}
          >
            <Trash2 className="mr-2 h-4 w-4" />
            Delete
          </Button>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Basic Information</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <p className="text-sm text-muted-foreground">Workflow ID</p>
              <p className="font-mono text-sm">{workflow.id}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Name</p>
              <p>{workflow.name}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Version</p>
              <p>v{workflow.version}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Deployment Status</p>
              <Badge variant={workflow.deployed ? 'default' : 'secondary'}>
                {workflow.deployed ? 'Deployed' : 'Draft'}
              </Badge>
              {workflow.deployed && workflow.deployed_at && (
                <p className="text-xs text-muted-foreground mt-1">
                  Deployed: {format(new Date(workflow.deployed_at), 'PPpp')}
                </p>
              )}
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Status</p>
              <Badge variant={workflow.enabled ? 'default' : 'secondary'}>
                {workflow.enabled ? 'Enabled' : 'Disabled'}
              </Badge>
            </div>
            {workflow.created_by && (
              <div>
                <p className="text-sm text-muted-foreground">Created By</p>
                <p>{workflow.created_by}</p>
              </div>
            )}
            <div>
              <p className="text-sm text-muted-foreground">Created At</p>
              <p>{format(new Date(workflow.created_at), 'PPpp')}</p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Workflow Structure</CardTitle>
            <CardDescription>Nodes and edges in this workflow</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <p className="text-sm text-muted-foreground">Total Nodes</p>
              <p className="text-2xl font-bold">{spec.nodes?.length || 0}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Total Edges</p>
              <p className="text-2xl font-bold">{spec.edges?.length || 0}</p>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Workflow Specification</CardTitle>
          <CardDescription>Complete workflow definition</CardDescription>
        </CardHeader>
        <CardContent>
          <pre className="bg-muted p-4 rounded-md overflow-auto text-sm">
            {JSON.stringify(spec, null, 2)}
          </pre>
        </CardContent>
      </Card>

      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Workflow</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete &quot;{workflow.name}&quot;? This action
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
              onClick={handleDelete}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={rollbackDialogOpen} onOpenChange={setRollbackDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rollback Workflow</DialogTitle>
            <DialogDescription>
              Select a previous deployed version to rollback to. This will create a new version
              with the selected version&apos;s specification.
            </DialogDescription>
          </DialogHeader>
          <div className="py-4">
            <p className="text-sm text-muted-foreground mb-2">
              Current version: v{workflow.version}
            </p>
            <p className="text-sm text-muted-foreground mb-4">
              Select a previous deployed version to rollback to:
            </p>
            <div className="space-y-2 max-h-60 overflow-y-auto">
              {previousDeployedVersions && previousDeployedVersions.length > 0 ? (
                previousDeployedVersions.map((v) => (
                  <Button
                    key={v}
                    variant={selectedRollbackVersion === v ? 'default' : 'outline'}
                    className="w-full justify-start"
                    onClick={() => setSelectedRollbackVersion(v)}
                  >
                    Version {v}
                  </Button>
                ))
              ) : (
                <p className="text-sm text-muted-foreground text-center py-4">
                  No previous deployed versions available
                </p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setRollbackDialogOpen(false);
                setSelectedRollbackVersion(null);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={() => {
                if (selectedRollbackVersion !== null) {
                  rollbackMutation.mutate(selectedRollbackVersion);
                }
              }}
              disabled={rollbackMutation.isPending || selectedRollbackVersion === null}
            >
              {rollbackMutation.isPending ? 'Rolling back...' : 'Rollback'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
