'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { configurationApi, type Configuration } from '@/lib/api-client';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Plus, Edit, Trash2 } from 'lucide-react';
import { format } from 'date-fns';
import { Badge } from '@/components/ui/badge';
import { useState } from 'react';
import { toast } from 'sonner';
import {
  LoadingState,
  ErrorState,
  PageHeader,
  DataTable,
  DeleteDialog,
  ActionButtons,
} from '@/components/shared';
import { ConfigurationDialog } from '@/components/configuration/configuration-dialog';

export default function ConfigurationsPage() {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [configToDelete, setConfigToDelete] = useState<Configuration | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [configToEdit, setConfigToEdit] = useState<Configuration | null>(null);
  const queryClient = useQueryClient();

  const { data: configurations, isLoading, error, isError } = useQuery({
    queryKey: ['configurations'],
    queryFn: () => configurationApi.list(),
    retry: 2,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => configurationApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['configurations'] });
      setDeleteDialogOpen(false);
      setConfigToDelete(null);
      toast.success('Configuration deleted successfully');
    },
    onError: (error) => {
      toast.error('Failed to delete configuration', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return <LoadingState message="Loading configurations..." />;
  }

  if (isError) {
    return (
      <ErrorState
        title="Configurations"
        description="Manage reusable configuration templates"
        error={error}
        queryKey={['configurations']}
      />
    );
  }

  const handleDelete = (config: Configuration) => {
    setConfigToDelete(config);
    setDeleteDialogOpen(true);
  };

  const confirmDelete = () => {
    if (configToDelete) {
      deleteMutation.mutate(configToDelete.id);
    }
  };

  const handleEdit = (config: Configuration) => {
    setConfigToEdit(config);
    setEditDialogOpen(true);
  };

  const handleCreate = () => {
    setCreateDialogOpen(true);
  };

  // Filter out duplicates by ID
  const uniqueConfigs = configurations?.filter((config, index, self) => 
    index === self.findIndex((c) => c.id === config.id)
  ) || [];

  const columns = [
    {
      key: 'name',
      header: 'Name',
      render: (config: Configuration) => (
        <span className="font-medium">{config.name}</span>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      render: (config: Configuration) => (
        <Badge variant="outline" className="badge-small">
          {config.type}
        </Badge>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      render: (config: Configuration) => (
        <span className="text-muted-foreground">{config.description || '-'}</span>
      ),
      className: 'table-cell-small',
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (config: Configuration) => (
        <span className="text-muted-foreground">
          {format(new Date(config.created_at), 'MMM d, yyyy')}
        </span>
      ),
      className: 'table-cell-small',
    },
    {
      key: 'actions',
      header: 'Actions',
      className: 'text-right',
      render: (config: Configuration) => (
        <ActionButtons
          actions={[
            {
              icon: Edit,
              label: 'Edit configuration',
              onClick: () => handleEdit(config),
            },
            {
              icon: Trash2,
              label: 'Delete configuration',
              variant: 'destructive',
              onClick: () => handleDelete(config),
            },
          ]}
        />
      ),
    },
  ];

  return (
    <>
      <Card>
        <PageHeader
          title="Configurations"
          description={`${uniqueConfigs.length} configuration${uniqueConfigs.length !== 1 ? 's' : ''} total • Manage reusable configuration templates for HTTP, databases, and more`}
          action={
            <Button size="sm" className="btn-small" onClick={handleCreate}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Configuration
            </Button>
          }
        />
        <CardContent className="page-content-padding">
          <DataTable
            data={uniqueConfigs}
            columns={columns}
            keyExtractor={(config, index) => `${config.id}-${index}`}
            emptyMessage="No configurations found"
            emptyAction={
              <Button size="sm" className="btn-small" onClick={handleCreate}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                Create Your First Configuration
              </Button>
            }
          />
        </CardContent>
      </Card>

      <DeleteDialog
        open={deleteDialogOpen}
        onOpenChange={setDeleteDialogOpen}
        title="Delete Configuration"
        description="Are you sure you want to delete &quot;{name}&quot;? This action cannot be undone."
        itemName={configToDelete?.name || ''}
        onConfirm={confirmDelete}
        isDeleting={deleteMutation.isPending}
      />

      <ConfigurationDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      {configToEdit && (
        <ConfigurationDialog
          open={editDialogOpen}
          onOpenChange={(open) => {
            setEditDialogOpen(open);
            if (!open) {
              setConfigToEdit(null);
            }
          }}
          configId={configToEdit.id}
        />
      )}
    </>
  );
}
