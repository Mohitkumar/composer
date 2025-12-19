import { CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { ReactNode } from 'react';

interface PageHeaderProps {
  title: string;
  description: string;
  action?: ReactNode;
}

export function PageHeader({ title, description, action }: PageHeaderProps) {
  return (
    <CardHeader className="page-header-padding">
      <div className="flex items-center justify-between gap-4">
        <div className="flex-1">
          <CardTitle className="page-title">{title}</CardTitle>
          <CardDescription className="page-description">{description}</CardDescription>
        </div>
        {action && <div className="flex items-center gap-2">{action}</div>}
      </div>
    </CardHeader>
  );
}
