import { create } from "zustand";
import { networkApi } from "../api/client";
import { Network, Peer } from "../types";

interface NetworkState {
  networks: Network[];
  selectedNetwork: Network | null;
  peers: Peer[];
  members: Peer[];
  loading: boolean;
  error: string;
  fetchNetworks: () => Promise<void>;
  createNetwork: (payload: { name: string; description: string }) => Promise<void>;
  fetchNetworkDetail: (networkId: string) => Promise<void>;
  fetchPeers: (networkId: string) => Promise<void>;
  fetchMembers: (networkId: string) => Promise<void>;
  approveMember: (networkId: string, memberId: string) => Promise<void>;
  rejectMember: (networkId: string, memberId: string) => Promise<void>;
  kickMember: (networkId: string, memberId: string) => Promise<void>;
}

export const useNetworkStore = create<NetworkState>((set) => ({
  networks: [],
  selectedNetwork: null,
  peers: [],
  members: [],
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
  },

  fetchMembers: async (networkId: string) => {
    try {
      const members = await networkApi.getMembers(networkId);
      set({ members });
    } catch {
      set({ error: "Failed to load members." });
    }
  },

  approveMember: async (networkId: string, memberId: string) => {
    try {
      await networkApi.approveMember(networkId, memberId);
      // Move from pending to approved — refetch both lists
      const [members, peers] = await Promise.all([
        networkApi.getMembers(networkId),
        networkApi.getPeers(networkId)
      ]);
      set({ members, peers });
    } catch {
      set({ error: "Failed to approve member." });
    }
  },

  rejectMember: async (networkId: string, memberId: string) => {
    try {
      await networkApi.rejectMember(networkId, memberId);
      set((state) => ({
        members: state.members.filter((m) => m.id !== memberId)
      }));
    } catch {
      set({ error: "Failed to reject member." });
    }
  },

  kickMember: async (networkId: string, memberId: string) => {
    try {
      await networkApi.kickMember(networkId, memberId);
      set((state) => ({
        peers: state.peers.filter((p) => p.id !== memberId),
        members: state.members.filter((m) => m.id !== memberId)
      }));
    } catch {
      set({ error: "Failed to remove member." });
    }
  }
}));
