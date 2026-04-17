import { create } from "zustand";
import { authApi, authStorage } from "../api/client";
import { User } from "../types";

interface AuthState {
  user: User | null;
  token: string;
  refreshToken: string;
  isAuthenticated: boolean;
  loading: boolean;
  error: string;
  login: (email: string, password: string) => Promise<void>;
  register: (name: string, email: string, password: string) => Promise<void>;
  refreshSession: () => Promise<void>;
  logout: () => void;
  hydrate: () => void;
}

const USER_KEY = "qt_user";

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  token: "",
  refreshToken: "",
  isAuthenticated: false,
  loading: false,
  error: "",

  hydrate: () => {
    const token = authStorage.getToken();
    const refreshToken = authStorage.getRefreshToken();
    const rawUser = localStorage.getItem(USER_KEY);
    const user = rawUser ? (JSON.parse(rawUser) as User) : null;

    set({
      token,
      refreshToken,
      user,
      isAuthenticated: Boolean(token && user)
    });
  },

  login: async (email, password) => {
    set({ loading: true, error: "" });
    try {
      const data = await authApi.login(email, password);
      localStorage.setItem(USER_KEY, JSON.stringify(data.user));
      set({
        user: data.user,
        token: data.access_token,
        refreshToken: data.refresh_token,
        isAuthenticated: true,
        loading: false,
        error: ""
      });
    } catch (error) {
      set({ loading: false, error: "Unable to log in. Check credentials and try again." });
      throw error;
    }
  },

  register: async (name, email, password) => {
    set({ loading: true, error: "" });
    try {
      await authApi.register(name, email, password);
      await useAuthStore.getState().login(email, password);
      set({ loading: false });
    } catch (error) {
      set({ loading: false, error: "Registration failed. Please verify your details." });
      throw error;
    }
  },

  refreshSession: async () => {
    try {
      const token = await authApi.refreshToken();
      set({ token, isAuthenticated: true });
    } catch {
      useAuthStore.getState().logout();
    }
  },

  logout: () => {
    authApi.logout();
    localStorage.removeItem(USER_KEY);
    set({
      user: null,
      token: "",
      refreshToken: "",
      isAuthenticated: false,
      loading: false,
      error: ""
    });
  }
}));
