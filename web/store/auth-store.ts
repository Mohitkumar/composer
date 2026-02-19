import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiClient } from '@/lib/api-client';

interface AuthState {
  isAuthenticated: boolean;
  user: { username: string; id: string; email?: string } | null;
  login: (username: string, password: string) => Promise<boolean>;
  logout: () => void;
  checkAuth: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      isAuthenticated: false,
      user: null,
      login: async (username: string, password: string) => {
        try {
          const response = await apiClient.post('/auth/login', {
            username,
            password,
          });
          
          const { token, user } = response.data;
          
          if (token && user) {
            if (typeof window !== 'undefined') {
              localStorage.setItem('auth_token', token);
              localStorage.setItem('auth_user', JSON.stringify({
                username: user.username,
                id: user.id,
                email: user.email,
              }));
            }
            set({
              isAuthenticated: true,
              user: {
                username: user.username,
                id: user.id,
                email: user.email,
              },
            });
            return true;
          }
          return false;
        } catch (error: any) {
          console.error('Login error:', error);
          return false;
        }
      },
      logout: async () => {
        try {
          // Call logout API (optional, mainly for token blacklisting in future)
          await apiClient.post('/auth/logout');
        } catch (error) {
          // Ignore logout API errors
        } finally {
          if (typeof window !== 'undefined') {
            localStorage.removeItem('auth_token');
          }
          set({
            isAuthenticated: false,
            user: null,
          });
        }
      },
      checkAuth: () => {
        if (typeof window !== 'undefined') {
          const token = localStorage.getItem('auth_token');
          if (!token) {
            set({
              isAuthenticated: false,
              user: null,
            });
            return;
          }

          // Token exists, assume authenticated
          // In a production app, you might want to validate the token
          // by calling a /auth/me endpoint
          const userStr = localStorage.getItem('auth_user');
          if (userStr) {
            try {
              const user = JSON.parse(userStr);
              set({
                isAuthenticated: true,
                user,
              });
            } catch {
              // Invalid user data, clear it
              localStorage.removeItem('auth_user');
              set({
                isAuthenticated: false,
                user: null,
              });
            }
          } else {
            set({
              isAuthenticated: false,
              user: null,
            });
          }
        }
      },
    }),
    {
      name: 'auth-storage',
      onRehydrateStorage: () => (state) => {
        if (state) {
          state.checkAuth();
        }
      },
    }
  )
);
