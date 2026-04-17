export default function LoadingSpinner({ label = "Loading..." }: { label?: string }) {
  return (
    <div className="flex items-center gap-3 text-ink/75">
      <span className="inline-block h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
      <span className="text-sm font-medium">{label}</span>
    </div>
  );
}
