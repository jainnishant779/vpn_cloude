import { useEffect, useMemo } from "react";
import { useParams } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import PeerList from "../components/PeerList";
import { useNetworkStore } from "../store/networkStore";
import { Peer } from "../types";

export default function NetworkDetail() {
  const { id = "" } = useParams();
  const {
    selectedNetwork, peers, members, loading, error,
    fetchNetworkDetail, fetchPeers, fetchMembers,
    approveMember, rejectMember, kickMember
  } = useNetworkStore();

  useEffect(() => {
    if (!id) return;
    void fetchNetworkDetail(id);
    void fetchPeers(id);
    void fetchMembers(id);
    // Auto-refresh every 10s
    const interval = setInterval(() => {
      void fetchMembers(id);
      void fetchPeers(id);
    }, 10000);
    return () => clearInterval(interval);
  }, [id, fetchNetworkDetail, fetchPeers, fetchMembers]);

  const onlinePeers = useMemo(() => peers.filter((p) => p.is_online).length, [peers]);
  const pendingMembers = useMemo(() => members.filter((m) => m.status === "pending"), [members]);
  const apiKey = localStorage.getItem("qt_api_key") ?? "";

  return (
    <div className="space-y-5 fade-in">
      {loading ? <LoadingSpinner label="Loading network" /> : null}
      {error ? <p className="text-sm text-ember">{error}</p> : null}

      <section className="rounded-xl2 border border-ink/10 bg-white/80 p-5">
        <h2 className="text-2xl font-semibold text-ink">{selectedNetwork?.name || "Network details"}</h2>
        <p className="mt-2 text-sm text-ink/70">{selectedNetwork?.description || "No description provided."}</p>

        <div className="mt-4 grid gap-3 md:grid-cols-3">
          <InfoChip label="Network ID" value={selectedNetwork?.network_id || "-"} />
          <InfoChip label="CIDR" value={selectedNetwork?.cidr || "-"} />
          <InfoChip label="Online peers" value={`${onlinePeers}/${peers.length}`} />
        </div>
      </section>

      {/* ── Pending Approval Requests ── */}
      {pendingMembers.length > 0 && (
        <section className="rounded-xl2 border-2 border-amber-400/60 bg-amber-50/80 p-5">
          <div className="flex items-center gap-2 mb-4">
            <span className="relative flex h-3 w-3">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75"></span>
              <span className="relative inline-flex rounded-full h-3 w-3 bg-amber-500"></span>
            </span>
            <h3 className="text-lg font-semibold text-amber-800">
              Pending Approval ({pendingMembers.length})
            </h3>
          </div>

          <div className="space-y-3">
            {pendingMembers.map((member) => (
              <PendingMemberCard
                key={member.id}
                member={member}
                onApprove={() => { if (id) void approveMember(id, member.id); }}
                onReject={() => { if (id) void rejectMember(id, member.id); }}
              />
            ))}
          </div>
        </section>
      )}

      <section className="rounded-xl2 border border-ink/10 bg-white/80 p-5">
        <h3 className="text-lg font-semibold text-ink">Client setup details</h3>
        <div className="mt-3 grid gap-3 md:grid-cols-2">
          <InfoChip label="API Key" value={apiKey || "Unavailable"} mono />
          <InfoChip label="Network Join ID" value={selectedNetwork?.network_id || "-"} mono />
        </div>
      </section>

      <section>
        <h3 className="mb-3 text-lg font-semibold text-ink">Peers</h3>
        <PeerList
          peers={peers}
          onConnectVNC={(peer) => {
            window.alert(`Use quicktunnel CLI: quicktunnel vnc ${peer.name || peer.machine_id}`);
          }}
          onRemove={(peer) => {
            if (id) void kickMember(id, peer.id);
          }}
        />
      </section>
    </div>
  );
}

function PendingMemberCard({
  member,
  onApprove,
  onReject
}: {
  member: Peer;
  onApprove: () => void;
  onReject: () => void;
}) {
  const timeAgo = member.created_at
    ? formatTimeAgo(new Date(member.created_at))
    : "just now";

  return (
    <div className="flex items-center justify-between rounded-lg border border-amber-200 bg-white px-4 py-3 shadow-sm">
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-full bg-amber-100 text-amber-600 text-sm font-bold">
          {(member.os || "?").charAt(0).toUpperCase()}
        </div>
        <div>
          <p className="font-semibold text-ink text-sm">{member.name || member.machine_id || "Unknown device"}</p>
          <p className="text-xs text-ink/50">
            {member.os || "unknown OS"} · requested {timeAgo}
          </p>
        </div>
      </div>

      <div className="flex gap-2">
        <button
          onClick={onApprove}
          className="rounded-lg bg-emerald-500 px-4 py-1.5 text-sm font-semibold text-white hover:bg-emerald-600 transition-colors shadow-sm"
        >
          ✓ Approve
        </button>
        <button
          onClick={onReject}
          className="rounded-lg bg-red-100 px-4 py-1.5 text-sm font-semibold text-red-600 hover:bg-red-200 transition-colors"
        >
          ✗ Reject
        </button>
      </div>
    </div>
  );
}

function InfoChip({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-lg border border-ink/10 bg-white px-3 py-2">
      <p className="text-xs uppercase tracking-[0.15em] text-ink/55">{label}</p>
      <p className={`mt-1 text-sm font-semibold text-ink ${mono ? "font-mono text-xs" : ""}`}>{value}</p>
    </div>
  );
}

function formatTimeAgo(date: Date): string {
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
