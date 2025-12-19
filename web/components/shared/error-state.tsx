import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { useQueryClient } from '@tanstack/react-query';

interface ErrorStateProps {
  title: string;
  description?: string;
  error?: Error | unknown;
  queryKey: string[];
  onRetry?: () => void;
}

export function ErrorState({ 
  title, 
  description, 
  error, 
  queryKey,
  onRetry 
}: ErrorStateProps) {
  const queryClient = useQueryClient();
  
  const handleRetry = () => {
    if (onRetry) {
      onRetry();
    } else {
      queryClient.invalidateQueries({ queryKey });
    }
  };

  const errorMessage = error instanceof Error ? error.message : 'Unknown error occurred';

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <div>
            <CardTitle>{title}</CardTitle>
            {description && <CardDescription>{description}</CardDescription>}
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-6">
        <div className="text-center py-12">
          <p className="text-destructive mb-2">Failed to load</p>
          <p className="text-sm text-muted-foreground mb-4">{errorMessage}</p>
          <Button onClick={handleRetry} size="sm">
            Retry
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
