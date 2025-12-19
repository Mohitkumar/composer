import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ReactNode } from 'react';

interface Column<T> {
  key: keyof T | string;
  header: string;
  render?: (item: T) => ReactNode;
  className?: string;
}

interface DataTableProps<T> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (item: T, index: number) => string;
  emptyMessage?: string;
  emptyAction?: ReactNode;
}

export function DataTable<T>({
  data,
  columns,
  keyExtractor,
  emptyMessage = "No items found",
  emptyAction,
}: DataTableProps<T>) {
  if (data.length === 0) {
    return (
      <div className="text-center py-12">
        <p className="text-muted-foreground text-sm mb-4">{emptyMessage}</p>
        {emptyAction}
      </div>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          {columns.map((column) => (
            <TableHead
              key={String(column.key)}
              className={`h-9 text-xs font-medium ${column.className || ''}`}
            >
              {column.header}
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {data.map((item, index) => (
          <TableRow key={keyExtractor(item, index)} className="h-11">
            {columns.map((column) => (
              <TableCell
                key={String(column.key)}
                className="py-2 text-sm"
              >
                {column.render
                  ? column.render(item)
                  : String(item[column.key as keyof T] ?? '-')}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
