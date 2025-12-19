import { CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { ReactNode } from 'react';

interface PageHeaderProps {
  title: string;
  description: string;
  action?: ReactNode;
}

export function PageHeader({ title, description, action }: PageHeaderProps) {
  return (
    <CardHeader className="pb-3">
      <div className="flex items-center justify-between gap-4">
        <div className="flex-1">
          <CardTitle className="text-xl">{title}</CardTitle>
          <CardDescription className="text-xs mt-0.5">{description}</CardDescription>
        </div>
        {action && <div className="flex items-center gap-2">{action}</div>}
      </div>
    </CardHeader>
  );
}
