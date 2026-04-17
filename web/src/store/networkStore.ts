import { create } from "zustand";
import { networkApi } from "../api/client";
import { Network, Peer } from "../types";

interface NetworkState {
  networks: Network[];
  selectedNetwork: Network | null;
  peers: Peer[];
  loading: boolean;
  error: string;
  fetchNetworks: () => Promise<void>;
  createNetwork: (payload: { name: string; description: string }) => Promise<void>;
  fetchNetworkDetail: (networkId: string) => Promise<void>;
  fetchPeers: (networkId: string) => Promise<void>;
}

export const useNetworkStore = create<NetworkState>((set) => ({
  networks: [],
  selectedNetwork: null,
  peers: [],
  loading: false,
  error: "",

  fetchNetworks: async () => {
    set({ loading: true, error: "" });
    try {
      const networks = await networkApi.getNetworks();
      set({ networks, loading: false });
    } catch {
      set({ loading: false, error: "Could not load networks." });
    }
  },

  createNetwork: async ({ name, description }) => {
    set({ loading: true, error: "" });
    try {
      const created = await networkApi.createNetwork({ name, description });
      set((state) => ({
        networks: [created, ...state.networks],
        loading: false
      }));
    } catch {
      set({ loading: false, error: "Failed to create network." });
    }
  },

  fetchNetworkDetail: async (networkId: string) => {
    set({ loading: true, error: "" });
    try {
      const result = await networkApi.getNetwork(networkId);
      set({ selectedNetwork: result.network, loading: false });
    } catch {
      set({ loading: false, error: "Failed to load network details." });
    }
  },

  fetchPeers: async (networkId: string) => {
    set({ loading: true, error: "" });
    try {
      const peers = await networkApi.getPeers(networkId);
      set({ peers, loading: false });
    } catch {
      set({ loading: false, error: "Failed to load peers." });
    }
  }
}));
