import { useEffect, useMemo } from "react";
import LoadingSpinner from "../components/LoadingSpinner";
import StatusBadge from "../components/StatusBadge";
import { useNetworkStore } from "../store/networkStore";

export default function Dashboard() {
  const { networks, peers, loading, fetchNetworks } = useNetworkStore();

  useEffect(() => {
    void fetchNetworks();
  }, [fetchNetworks]);

  const totalNetworks = networks.length;
  const totalPeers = peers.length;
  const onlinePeers = useMemo(() => peers.filter((peer) => peer.is_online).length, [peers]);

  return (
    <div className="space-y-6 fade-in">
      <section className="grid gap-4 md:grid-cols-3">
        <MetricCard title="Total Networks" value={String(totalNetworks)} subtitle="Managed environments" tone="accent" />
        <MetricCard title="Total Peers" value={String(totalPeers)} subtitle="Registered machines" tone="gold" />
        <MetricCard title="Online Peers" value={String(onlinePeers)} subtitle="Currently reachable" tone="ember" />
      </section>

      <section className="card-surface rounded-xl2 p-5">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-xl font-semibold text-ink">Recent Activity</h2>
          <StatusBadge status={loading ? "connecting" : "online"} />
        </div>
        {loading ? (
          <LoadingSpinner label="Refreshing dashboard metrics" />
        ) : (
          <ul className="space-y-2 text-sm text-ink/75">
            <li>Network inventory synchronized.</li>
            <li>Peer heartbeat status fetched from control plane.</li>
            <li>Tunnel map ready for VNC launch requests.</li>
          </ul>
        )}
      </section>
    </div>
  );
}

function MetricCard({
  title,
  value,
  subtitle,
  tone
}: {
  title: string;
  value: string;
  subtitle: string;
  tone: "accent" | "gold" | "ember";
}) {
  const toneClass =
    tone === "accent"
      ? "from-accent/20 to-accent/5"
      : tone === "gold"
      ? "from-gold/25 to-gold/8"
      : "from-ember/20 to-ember/5";

  return (
    <article className={`rounded-xl2 border border-ink/10 bg-gradient-to-br ${toneClass} p-5`}>
      <p className="text-xs uppercase tracking-[0.16em] text-ink/60">{title}</p>
      <p className="mt-2 text-3xl font-semibold text-ink">{value}</p>
      <p className="mt-1 text-sm text-ink/70">{subtitle}</p>
    </article>
  );
}
