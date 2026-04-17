export interface User {
  id: string;
  email: string;
  name: string;
}

export interface Network {
  id: string;
  owner_id: string;
  name: string;
  network_id: string;
  cidr: string;
  description: string;
  max_peers: number;
  is_active: boolean;
  created_at: string;
}

export interface Peer {
  id: string;
  network_id: string;
  name: string;
  machine_id: string;
  public_key: string;
  virtual_ip: string;
  public_endpoint: string;
  local_endpoints: string[];
  os: string;
  version: string;
  is_online: boolean;
  vnc_port: number;
  vnc_available: boolean;
  relay_id: string;
  last_seen?: string;
}

export interface ApiEnvelope<T> {
  success: boolean;
  data: T;
  error: string;
}
