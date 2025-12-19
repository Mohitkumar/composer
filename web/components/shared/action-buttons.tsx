import { Button } from '@/components/ui/button';
import { LucideIcon } from 'lucide-react';
import Link from 'next/link';
import { cn } from '@/lib/utils';

interface ActionButton {
  icon: LucideIcon;
  label: string;
  onClick?: () => void;
  href?: string;
  variant?: 'default' | 'ghost' | 'destructive';
  disabled?: boolean;
}

interface ActionButtonsProps {
  actions: ActionButton[];
}

export function ActionButtons({ actions }: ActionButtonsProps) {
  return (
    <div className="flex items-center justify-end gap-1">
      {actions.map((action, index) => {
        const Icon = action.icon;
        const isDestructive = action.variant === 'destructive';
        const buttonProps = {
          variant: 'ghost' as const,
          size: 'sm' as const,
          className: cn(
            'btn-icon',
            isDestructive && 'text-destructive hover:text-destructive hover:bg-destructive/10'
          ),
          title: action.label,
          disabled: action.disabled,
          ...(action.onClick && { onClick: action.onClick }),
        };

        if (action.href) {
          return (
            <Button key={index} {...buttonProps} asChild>
              <Link href={action.href}>
                <Icon className="btn-icon-size shrink-0" />
              </Link>
            </Button>
          );
        }

        return (
          <Button key={index} {...buttonProps}>
            <Icon className="btn-icon-size shrink-0" />
          </Button>
        );
      })}
    </div>
  );
}
