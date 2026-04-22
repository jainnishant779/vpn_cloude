import { Peer } from "../types";
import StatusBadge from "./StatusBadge";

interface Props {
  peers: Peer[];
  onConnectVNC?: (peer: Peer) => void;
  onRemove?: (peer: Peer) => void;
}

export default function PeerList({ peers, onConnectVNC, onRemove }: Props) {
  return (
    <div className="overflow-x-auto rounded-xl2 border border-ink/10 bg-white/80">
      <table className="min-w-full text-sm">
        <thead className="bg-ink/5 text-left text-xs uppercase tracking-wide text-ink/70">
          <tr>
            <th className="px-4 py-3">Name</th>
            <th className="px-4 py-3">Virtual IP</th>
            <th className="px-4 py-3">Status</th>
            <th className="px-4 py-3">OS</th>
            <th className="px-4 py-3">VNC</th>
            <th className="px-4 py-3">Last Seen</th>
            <th className="px-4 py-3">VNC</th>
            <th className="px-4 py-3">Actions</th>
          </tr>
        </thead>
        <tbody>
          {peers.map((peer) => (
            <tr key={peer.id} className="border-t border-ink/5">
              <td className="px-4 py-3 font-medium">{peer.name || peer.machine_id}</td>
              <td className="px-4 py-3 text-ink/70">{peer.virtual_ip}</td>
              <td className="px-4 py-3">
                <StatusBadge status={peer.is_online ? "online" : "offline"} />
              </td>
              <td className="px-4 py-3 text-ink/70">{peer.os || "Unknown"}</td>
              <td className="px-4 py-3">{peer.vnc_available ? `:${peer.vnc_port}` : "Not available"}</td>
              <td className="px-4 py-3 text-ink/70">{peer.last_seen || "-"}</td>
              <td className="px-4 py-3">
                <button
                  type="button"
                  disabled={!peer.is_online || !peer.vnc_available}
                  onClick={() => onConnectVNC?.(peer)}
                  className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:bg-zinc-300"
                >
                  Connect VNC
                </button>
              </td>
              <td className="px-4 py-3">
                <button
                  type="button"
                  onClick={() => { if(window.confirm("Remove " + peer.name + "?")) onRemove?.(peer); }}
                  className="rounded-lg bg-red-500 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-600"
                >
                  Remove
                </button>
              </td>
            </tr>
          ))}

          {peers.length === 0 && (
            <tr>
              <td colSpan={7} className="px-4 py-6 text-center text-ink/60">
                No peers found in this network.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
