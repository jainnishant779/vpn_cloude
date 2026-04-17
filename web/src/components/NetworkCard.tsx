import { Link } from "react-router-dom";
import { Network } from "../types";

interface Props {
  network: Network;
  peerCount?: number;
}

export default function NetworkCard({ network, peerCount = 0 }: Props) {
  return (
    <Link
      to={`/networks/${network.id}`}
      className="card-surface group block rounded-xl2 p-5 transition hover:-translate-y-1 hover:shadow-card"
    >
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-ink">{network.name}</h3>
        <span className="rounded-full bg-accent/10 px-2 py-1 text-xs font-semibold text-accent">
          {network.is_active ? "Active" : "Inactive"}
        </span>
      </div>
      <p className="mt-2 text-sm text-ink/70">{network.description || "No description"}</p>

      <div className="mt-4 grid grid-cols-2 gap-2 text-xs text-ink/70">
        <div>
          <p className="font-semibold text-ink">Network ID</p>
          <p>{network.network_id}</p>
        </div>
        <div>
          <p className="font-semibold text-ink">Peers</p>
          <p>{peerCount}</p>
        </div>
      </div>
    </Link>
  );
}
