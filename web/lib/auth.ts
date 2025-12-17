// Simple auth store - in production, use proper auth with JWT tokens
export interface AuthState {
  isAuthenticated: boolean;
  user: {
    username: string;
  } | null;
}

// Simple mock auth - replace with real auth in production
export const auth = {
  login: async (username: string, password: string): Promise<boolean> => {
    // Mock authentication - in production, call your auth API
    if (username && password) {
      localStorage.setItem('auth_token', 'mock_token_' + username);
      localStorage.setItem('auth_user', username);
      return true;
    }
    return false;
  },

  logout: () => {
    localStorage.removeItem('auth_token');
    localStorage.removeItem('auth_user');
  },

  getCurrentUser: (): string | null => {
    if (typeof window === 'undefined') return null;
    return localStorage.getItem('auth_user');
  },

  isAuthenticated: (): boolean => {
    if (typeof window === 'undefined') return false;
    return !!localStorage.getItem('auth_token');
  },
};
