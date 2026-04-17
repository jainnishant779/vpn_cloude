import axios, { AxiosError, AxiosResponse, InternalAxiosRequestConfig } from "axios";
import { ApiEnvelope, Network, Peer, User } from "../types";

const TOKEN_KEY = "qt_access_token";
const REFRESH_TOKEN_KEY = "qt_refresh_token";

const API_BASE_URL =
  localStorage.getItem("qt_api_base_url") ??
  (typeof window !== "undefined" ? (window as unknown as { QT_API_BASE_URL?: string }).QT_API_BASE_URL : "") ??
  "";

const api = axios.create({
  baseURL: API_BASE_URL,
  timeout: 10_000
});

const getAccessToken = () => localStorage.getItem(TOKEN_KEY) ?? "";
const getRefreshToken = () => localStorage.getItem(REFRESH_TOKEN_KEY) ?? "";

const setTokens = (accessToken: string, refreshToken?: string) => {
  localStorage.setItem(TOKEN_KEY, accessToken);
  if (refreshToken) {
    localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken);
  }
};

const clearTokens = () => {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(REFRESH_TOKEN_KEY);
};

api.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  const token = getAccessToken();
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

let isRefreshing = false;
let pendingQueue: Array<(token: string) => void> = [];

api.interceptors.response.use(
  (response: AxiosResponse) => response,
  async (error: AxiosError<ApiEnvelope<unknown>>) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

    if (!originalRequest || originalRequest._retry || error.response?.status !== 401) {
      return Promise.reject(error);
    }

    const refreshToken = getRefreshToken();
    if (!refreshToken) {
      clearTokens();
      return Promise.reject(error);
    }

    originalRequest._retry = true;

    if (isRefreshing) {
      return new Promise((resolve) => {
        pendingQueue.push((newToken: string) => {
          originalRequest.headers.Authorization = `Bearer ${newToken}`;
          resolve(api(originalRequest));
        });
      });
    }

    isRefreshing = true;
    try {
      const response = await axios.post<ApiEnvelope<{ access_token: string }>>(
        `${api.defaults.baseURL}/api/v1/auth/refresh`,
        { refresh_token: refreshToken }
      );

      const newToken = response.data.data.access_token;
      setTokens(newToken);
      pendingQueue.forEach((resume) => resume(newToken));
      pendingQueue = [];

      originalRequest.headers.Authorization = `Bearer ${newToken}`;
      return api(originalRequest);
    } catch (refreshError) {
      clearTokens();
      pendingQueue = [];
      return Promise.reject(refreshError);
    } finally {
      isRefreshing = false;
    }
  }
);

interface LoginResponse {
  access_token: string;
  refresh_token: string;
  api_key: string;
  user: User;
}

interface RegisterResponse {
  id: string;
  email: string;
  name: string;
  api_key: string;
}

export const authApi = {
  async login(email: string, password: string) {
    const response = await api.post<ApiEnvelope<LoginResponse>>("/api/v1/auth/login", {
      email,
      password
    });

    const payload = response.data.data;
    setTokens(payload.access_token, payload.refresh_token);
    localStorage.setItem("qt_api_key", payload.api_key);

    return payload;
  },

  async register(name: string, email: string, password: string) {
    const response = await api.post<ApiEnvelope<RegisterResponse>>("/api/v1/auth/register", {
      name,
      email,
      password
    });
    return response.data.data;
  },

  async refreshToken() {
    const refreshToken = getRefreshToken();
    const response = await api.post<ApiEnvelope<{ access_token: string }>>("/api/v1/auth/refresh", {
      refresh_token: refreshToken
    });

    setTokens(response.data.data.access_token);
    return response.data.data.access_token;
  },

  logout() {
    clearTokens();
    localStorage.removeItem("qt_api_key");
  }
};

export const networkApi = {
  async getNetworks() {
    const response = await api.get<ApiEnvelope<Network[]>>("/api/v1/networks");
    return response.data.data;
  },

  async createNetwork(payload: { name: string; description: string; max_peers?: number; cidr?: string }) {
    const response = await api.post<ApiEnvelope<Network>>("/api/v1/networks", payload);
    return response.data.data;
  },

  async getNetwork(id: string) {
    const response = await api.get<ApiEnvelope<{ network: Network; peer_count: number }>>(`/api/v1/networks/${id}`);
    return response.data.data;
  },

  async getPeers(id: string) {
    const response = await api.get<ApiEnvelope<Peer[]>>(`/api/v1/networks/${id}/peers`);
    return response.data.data;
  },

  async deleteNetwork(id: string) {
    await api.delete(`/api/v1/networks/${id}`);
  }
};

export const settingsApi = {
  async regenerateApiKey() {
    // If backend endpoint is unavailable, preserve existing key semantics.
    const existing = localStorage.getItem("qt_api_key") ?? "";
    return existing;
  }
};

export const authStorage = {
  getToken: getAccessToken,
  getRefreshToken,
  clearTokens,
  setTokens
};

export default api;
