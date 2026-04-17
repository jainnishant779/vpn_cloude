import { Gauge, Network as NetworkIcon, Settings } from "lucide-react";
import { NavLink } from "react-router-dom";

const navItems = [
  { to: "/dashboard", label: "Dashboard", icon: Gauge },
  { to: "/networks", label: "Networks", icon: NetworkIcon },
  { to: "/settings", label: "Settings", icon: Settings }
];

export default function Sidebar() {
  return (
    <aside className="card-surface flex w-full flex-col rounded-xl2 p-4 lg:w-64">
      <div className="mb-6 rounded-xl bg-accent px-4 py-4 text-white">
        <p className="text-xs uppercase tracking-[0.2em] text-white/75">QuickTunnel</p>
        <p className="mt-1 text-xl font-semibold">Control Deck</p>
      </div>

      <nav className="space-y-2">
        {navItems.map((item) => {
          const Icon = item.icon;
          return (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition ${
                  isActive ? "bg-ink text-white" : "text-ink/80 hover:bg-ink/10"
                }`
              }
            >
              <Icon size={17} />
              {item.label}
            </NavLink>
          );
        })}
      </nav>
    </aside>
  );
}
