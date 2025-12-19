'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { configurationApi, type Configuration } from '@/lib/api-client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Plus, Edit, Trash2 } from 'lucide-react';
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

export default function ConfigurationsPage() {
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [configToDelete, setConfigToDelete] = useState<Configuration | null>(null);
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
    },
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading configurations...</p>
        </div>
      </div>
    );
  }

  if (isError) {
    return (
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>Configurations</CardTitle>
              <CardDescription>
                Manage reusable configuration templates
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent className="pt-6">
          <div className="text-center py-12">
            <p className="text-destructive mb-2">Failed to load configurations</p>
            <p className="text-sm text-muted-foreground mb-4">
              {error instanceof Error ? error.message : 'Unknown error occurred'}
            </p>
            <Button
              onClick={() => queryClient.invalidateQueries({ queryKey: ['configurations'] })}
              size="sm"
            >
              Retry
            </Button>
          </div>
        </CardContent>
      </Card>
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

  // Filter out duplicates by ID
  const uniqueConfigs = configurations?.filter((config, index, self) => 
    index === self.findIndex((c) => c.id === config.id)
  ) || [];

  return (
    <>
      <Card>
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between gap-4">
            <div className="flex-1">
              <CardTitle className="text-xl">Configurations</CardTitle>
              <CardDescription className="text-xs mt-0.5">
                {uniqueConfigs.length} configuration{uniqueConfigs.length !== 1 ? 's' : ''} total • Manage reusable configuration templates for S3, HTTP, databases, and more
              </CardDescription>
            </div>
            <Link href="/configurations/new">
              <Button size="sm" className="h-8">
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                Create Configuration
              </Button>
            </Link>
          </div>
        </CardHeader>
        <CardContent className="pt-0">
          {uniqueConfigs.length === 0 ? (
            <div className="text-center py-12">
              <p className="text-muted-foreground mb-4 text-sm">No configurations found</p>
              <Link href="/configurations/new">
                <Button size="sm" className="h-8">
                  <Plus className="mr-1.5 h-3.5 w-3.5" />
                  Create Your First Configuration
                </Button>
              </Link>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="h-9 text-xs font-medium">Name</TableHead>
                    <TableHead className="h-9 text-xs font-medium">Type</TableHead>
                    <TableHead className="h-9 text-xs font-medium">Description</TableHead>
                    <TableHead className="h-9 text-xs font-medium">Created</TableHead>
                    <TableHead className="h-9 text-xs font-medium text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {uniqueConfigs.map((config, index) => (
                    <TableRow key={`${config.id}-${index}`} className="h-11">
                      <TableCell className="py-2 text-sm font-medium">
                        {config.name}
                      </TableCell>
                      <TableCell className="py-2">
                        <Badge variant="outline" className="text-xs">{config.type}</Badge>
                      </TableCell>
                      <TableCell className="py-2 text-xs text-muted-foreground">
                        {config.description || '-'}
                      </TableCell>
                      <TableCell className="py-2 text-xs text-muted-foreground">
                        {format(new Date(config.created_at), 'MMM d, yyyy')}
                      </TableCell>
                      <TableCell className="py-2">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            asChild
                            title="Edit configuration"
                          >
                            <Link href={`/configurations/${config.id}/edit`}>
                              <Edit className="h-3.5 w-3.5" />
                            </Link>
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                            onClick={() => handleDelete(config)}
                            title="Delete configuration"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </>
          )}
        </CardContent>
      </Card>

      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Configuration</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete &quot;{configToDelete?.name}&quot;? This action
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
    </>
  );
}
