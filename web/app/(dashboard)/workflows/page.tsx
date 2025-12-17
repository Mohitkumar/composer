'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { workflowApi, type WorkflowDefinition } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Plus, Edit, Trash2, Play } from 'lucide-react';
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
import { useState } from 'react';
import { useRouter } from 'next/navigation';

export default function WorkflowsPage() {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [workflowToDelete, setWorkflowToDelete] = useState<WorkflowDefinition | null>(null);
  const [page, setPage] = useState(1);
  const pageSize = 10;
  const router = useRouter();
  const queryClient = useQueryClient();

  const { data: workflows, isLoading, error, isError } = useQuery({
    queryKey: ['workflows'],
    queryFn: () => workflowApi.list(),
    retry: 2,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => workflowApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setDeleteDialogOpen(false);
      setWorkflowToDelete(null);
    },
  });

  const executeMutation = useMutation({
    mutationFn: (id: string) =>
      workflowApi.execute(id, {
        workflow_version: 1,
        input: {},
      }),
    onSuccess: (data) => {
      router.push(`/executions/${data.run_id}`);
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

  // Filter out duplicates by ID
  const uniqueWorkflows = workflows?.filter((workflow, index, self) => 
    index === self.findIndex((w) => w.id === workflow.id)
  ) || [];
  
  const paginatedWorkflows = uniqueWorkflows.slice(
    (page - 1) * pageSize,
    page * pageSize
  );
  const totalPages = Math.ceil(uniqueWorkflows.length / pageSize);

  const handleDelete = (workflow: WorkflowDefinition) => {
    setWorkflowToDelete(workflow);
    setDeleteDialogOpen(true);
  };

  const confirmDelete = () => {
    if (workflowToDelete) {
      deleteMutation.mutate(workflowToDelete.id);
    }
  };

  const handleExecute = (workflow: WorkflowDefinition) => {
    executeMutation.mutate(workflow.id);
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
          <CardTitle>All Workflows</CardTitle>
          <CardDescription>
            {workflows?.length || 0} workflow{workflows?.length !== 1 ? 's' : ''} total
          </CardDescription>
        </CardHeader>
        <CardContent>
          {paginatedWorkflows.length === 0 ? (
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
                    <TableHead>Created</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {paginatedWorkflows.map((workflow, index) => (
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
                            disabled={executeMutation.isPending}
                          >
                            <Play className="h-4 w-4" />
                          </Button>
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
                    Page {page} of {totalPages}
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                      disabled={page === 1}
                    >
                      Previous
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                      disabled={page === totalPages}
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
    </div>
  );
}
