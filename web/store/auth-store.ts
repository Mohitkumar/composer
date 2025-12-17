import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface AuthState {
  isAuthenticated: boolean;
  user: { username: string } | null;
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
        // Mock auth - replace with real API call
        if (username && password) {
          const token = 'mock_token_' + username;
          if (typeof window !== 'undefined') {
            localStorage.setItem('auth_token', token);
            localStorage.setItem('auth_user', username);
          }
          set({
            isAuthenticated: true,
            user: { username },
          });
          return true;
        }
        return false;
      },
      logout: () => {
        if (typeof window !== 'undefined') {
          localStorage.removeItem('auth_token');
        }
        set({
          isAuthenticated: false,
          user: null,
        });
      },
      checkAuth: () => {
        if (typeof window !== 'undefined') {
          const token = localStorage.getItem('auth_token');
          const user = localStorage.getItem('auth_user');
          if (token && user) {
            set({
              isAuthenticated: true,
              user: { username: user },
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
