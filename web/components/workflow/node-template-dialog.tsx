"use client";

import type { NodeTemplate } from "@/lib/api-client";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface NodeTemplateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  nodeTemplates: NodeTemplate[];
  isLoading: boolean;
  selectedRuntime: string;
  onRuntimeChange: (runtime: string) => void;
  onSelectTemplate: (template: NodeTemplate) => void;
}

export function NodeTemplateDialog({
  open,
  onOpenChange,
  nodeTemplates,
  isLoading,
  selectedRuntime,
  onRuntimeChange,
  onSelectTemplate,
}: NodeTemplateDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Insert Node Template</DialogTitle>
          <DialogDescription>
            Select a node template to insert into your workflow
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Filter by Runtime</Label>
            <Select
              value={selectedRuntime || "all"}
              onValueChange={(value) => onRuntimeChange(value === "all" ? "" : value)}
            >
              <SelectTrigger>
                <SelectValue placeholder="All runtimes" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All runtimes</SelectItem>
                <SelectItem value="http">HTTP</SelectItem>
                <SelectItem value="javascript">JavaScript</SelectItem>
                <SelectItem value="python">Python</SelectItem>
                <SelectItem value="go">Go</SelectItem>
                <SelectItem value="sql">SQL</SelectItem>
                <SelectItem value="s3">S3</SelectItem>
                <SelectItem value="csv">CSV</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="max-h-[400px] overflow-y-auto space-y-2">
            {isLoading ? (
              <p className="text-sm text-muted-foreground">Loading templates...</p>
            ) : nodeTemplates.length === 0 ? (
              <p className="text-sm text-muted-foreground">No templates found</p>
            ) : (
              nodeTemplates.map((template) => (
                <div
                  key={template.id}
                  className="p-3 border rounded-md hover:bg-accent cursor-pointer"
                  onClick={() => onSelectTemplate(template)}
                >
                  <div className="font-medium">{template.name}</div>
                  {template.description && (
                    <div className="text-sm text-muted-foreground mt-1">
                      {template.description}
                    </div>
                  )}
                  <div className="text-xs text-muted-foreground mt-1">
                    Runtime: {template.runtime} | Type: {template.node_type}
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
