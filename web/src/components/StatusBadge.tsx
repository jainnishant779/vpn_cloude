type Status = "online" | "offline" | "connecting";

const STYLE_MAP: Record<Status, string> = {
  online: "bg-emerald-100 text-emerald-700 border-emerald-200",
  offline: "bg-zinc-100 text-zinc-600 border-zinc-200",
  connecting: "bg-amber-100 text-amber-700 border-amber-200"
};

export default function StatusBadge({ status }: { status: Status }) {
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-semibold uppercase tracking-wide ${
        STYLE_MAP[status]
      }`}
    >
      {status}
    </span>
  );
}
