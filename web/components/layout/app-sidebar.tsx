'use client';

import {
  Home,
  Play,
  Workflow,
  LogOut,
  Key,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarHeader,
  SidebarFooter,
  SidebarTrigger,
  useSidebar,
} from '@/components/ui/sidebar';
import { useAuthStore } from '@/store/auth-store';
import { Button } from '@/components/ui/button';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';

const menuItems = [
  {
    title: 'Home',
    url: '/',
    icon: Home,
  },
  {
    title: 'Workflows',
    url: '/workflows',
    icon: Workflow,
  },
  {
    title: 'Executions',
    url: '/executions',
    icon: Play,
  },
  {
    title: 'Configurations',
    url: '/configurations',
    icon: Key,
  },
];

function SidebarContentWrapper() {
  const pathname = usePathname();
  const { state } = useSidebar();
  const isCollapsed = state === 'collapsed';

  return (
    <SidebarContent>
      <SidebarGroup>
        <SidebarGroupLabel>Menu</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            {menuItems.map((item) => {
              const Icon = item.icon;
              const isActive = pathname === item.url;
              return (
                <SidebarMenuItem key={item.url}>
                  <SidebarMenuButton
                    asChild
                    isActive={isActive}
                    tooltip={item.title}
                  >
                    <Link href={item.url}>
                      <Icon />
                      <span>{item.title}</span>
                    </Link>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              );
            })}
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
  );
}

function SidebarHeaderWrapper() {
  const { state } = useSidebar();
  const isCollapsed = state === 'collapsed';

  return (
    <SidebarHeader>
      <div className="flex items-center gap-2 px-2">
        <SidebarTrigger />
        {!isCollapsed && (
          <h1 className="text-lg font-semibold">Composer</h1>
        )}
      </div>
    </SidebarHeader>
  );
}

function SidebarFooterWrapper() {
  const router = useRouter();
  const { user, logout } = useAuthStore();
  const { state } = useSidebar();
  const isCollapsed = state === 'collapsed';

  const handleLogout = () => {
    logout();
    router.push('/login');
  };

  const userInitials = user?.username
    ?.split(' ')
    .map((n) => n[0])
    .join('')
    .toUpperCase()
    .slice(0, 2) || 'U';

  return (
    <SidebarFooter>
      <div className={`flex items-center p-2 gap-2 ${isCollapsed ? 'justify-center' : 'justify-between'}`}>
        <div className="flex items-center gap-2 min-w-0">
          <Avatar className="h-8 w-8 shrink-0">
            <AvatarFallback>{userInitials}</AvatarFallback>
          </Avatar>
          {!isCollapsed && (
            <span className="text-sm font-medium truncate">
              {user?.username || 'User'}
            </span>
          )}
        </div>
        {!isCollapsed && (
          <Button variant="ghost" size="sm" className="h-8 w-8 p-0 shrink-0" onClick={handleLogout} title="Log out">
            <LogOut className="h-4 w-4" />
          </Button>
        )}
      </div>
    </SidebarFooter>
  );
}

export function AppSidebar() {
  return (
    <Sidebar collapsible="icon">
      <SidebarHeaderWrapper />
      <SidebarContentWrapper />
      <SidebarFooterWrapper />
    </Sidebar>
  );
}
