import { useState } from "react";
import { Copy, Check, Terminal, Monitor, Globe } from "lucide-react";

interface Props {
  networkId: string;
}

type Tab = "windows" | "linux" | "mac";

export default function JoinCommand({ networkId }: Props) {
  const [activeTab, setActiveTab] = useState<Tab>("windows");
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const serverUrl = `${window.location.protocol}//${window.location.host}`;

  // Direct curl commands — no exe, no install needed
  const commands: Record<Tab, { label: string; icon: string; cmd: string }> = {
    windows: {
      label: "Windows (CMD)",
      icon: "🪟",
      cmd: `curl -X POST ${serverUrl}/api/v1/join ^
  -H "Content-Type: application/json" ^
  -d "{\\"network_id\\":\\"${networkId}\\",\\"hostname\\":\\"%COMPUTERNAME%\\",\\"wg_public_key\\":\\"%COMPUTERNAME%_key\\",\\"os\\":\\"windows\\"}"`,
    },
    linux: {
      label: "Linux",
      icon: "🐧",
      cmd: `curl -X POST ${serverUrl}/api/v1/join \\
  -H "Content-Type: application/json" \\
  -d '{"network_id":"${networkId}","hostname":"'$(hostname)'","wg_public_key":"'$(hostname)'_key","os":"linux"}'`,
    },
    mac: {
      label: "macOS",
      icon: "🍎",
      cmd: `curl -X POST ${serverUrl}/api/v1/join \\
  -H "Content-Type: application/json" \\
  -d '{"network_id":"${networkId}","hostname":"'$(hostname)'","wg_public_key":"'$(hostname)'_key","os":"darwin"}'`,
    },
  };

  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      document.body.removeChild(ta);
    }
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 2000);
  }

  const active = commands[activeTab];

  return (
    <div className="rounded-xl2 border border-ink/10 bg-ink overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-2 px-5 pt-4 pb-3 border-b border-white/10">
        <Terminal size={16} className="text-accent" />
        <span className="text-sm font-semibold text-white/90">
          Join this network — No installation needed, just paste &amp; run
        </span>
      </div>

      {/* OS Tabs */}
      <div className="flex gap-1 px-4 pt-3">
        {(Object.keys(commands) as Tab[]).map((tab) => (
          <button
            key={tab}
            type="button"
            onClick={() => setActiveTab(tab)}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition ${
              activeTab === tab
                ? "bg-accent text-white"
                : "text-white/50 hover:text-white/80 hover:bg-white/5"
            }`}
          >
            {commands[tab].icon} {commands[tab].label}
          </button>
        ))}
      </div>

      {/* Command Box */}
      <div className="p-4 space-y-3">
        <p className="text-xs text-white/40 uppercase tracking-wider">
          Run this in {active.label} terminal → wait for admin approval
        </p>

        <div className="relative">
          <pre className="rounded-lg bg-white/5 border border-white/10 px-4 py-3 text-sm text-green-400 font-mono whitespace-pre-wrap break-all leading-relaxed">
            {active.cmd}
          </pre>
          <button
            type="button"
            onClick={() => copy(activeTab, active.cmd)}
            className="absolute top-2 right-2 flex items-center gap-1.5 rounded-md bg-accent/80 hover:bg-accent px-2.5 py-1.5 text-xs font-semibold text-white transition"
          >
            {copiedKey === activeTab ? <><Check size={12} /> Copied!</> : <><Copy size={12} /> Copy</>}
          </button>
        </div>

        {/* Steps */}
        <div className="grid grid-cols-3 gap-2 pt-1">
          {[
            { icon: <Monitor size={13} />, step: "1. Open terminal / CMD" },
            { icon: <Globe size={13} />, step: "2. Paste & run command" },
            { icon: <Check size={13} />, step: "3. Admin approves below ↓" },
          ].map(({ icon, step }) => (
            <div
              key={step}
              className="flex items-center gap-1.5 rounded-lg bg-white/5 px-2.5 py-2 text-xs text-white/60"
            >
              <span className="text-accent shrink-0">{icon}</span>
              {step}
            </div>
          ))}
        </div>

        {/* Network ID copy */}
        <div className="flex items-center justify-between rounded-lg bg-white/5 px-3 py-2">
          <span className="text-xs text-white/40 uppercase tracking-wider">Network ID</span>
          <div className="flex items-center gap-2">
            <code className="text-sm font-mono text-white/80">{networkId}</code>
            <button
              type="button"
              onClick={() => copy("nid", networkId)}
              className="text-white/40 hover:text-accent transition"
            >
              {copiedKey === "nid" ? <Check size={13} /> : <Copy size={13} />}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
