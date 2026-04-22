import { useEffect, useMemo } from "react";
import { useParams } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import PeerList from "../components/PeerList";
import { useNetworkStore } from "../store/networkStore";

export default function NetworkDetail() {
  const { id = "" } = useParams();
  const { selectedNetwork, peers, loading, error, fetchNetworkDetail, fetchPeers, kickMember } = useNetworkStore();

  useEffect(() => {
    if (!id) return;
    void fetchNetworkDetail(id);
    void fetchPeers(id);
  }, [id, fetchNetworkDetail, fetchPeers]);

  const onlinePeers = useMemo(() => peers.filter((peer) => peer.is_online).length, [peers]);
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

function InfoChip({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-lg border border-ink/10 bg-white px-3 py-2">
      <p className="text-xs uppercase tracking-[0.15em] text-ink/55">{label}</p>
      <p className={`mt-1 text-sm font-semibold text-ink ${mono ? "font-mono text-xs" : ""}`}>{value}</p>
    </div>
  );
}
