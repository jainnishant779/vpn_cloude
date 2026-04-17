import { LogOut } from "lucide-react";
import { Outlet } from "react-router-dom";
import { useAuthStore } from "../store/authStore";
import Sidebar from "./Sidebar";

export default function Layout() {
  const { user, logout } = useAuthStore();

  return (
    <div className="mx-auto min-h-screen max-w-7xl p-4 md:p-6">
      <div className="grid gap-4 lg:grid-cols-[16rem_1fr]">
        <Sidebar />

        <main className="card-surface rounded-xl2 p-5 md:p-6">
          <header className="mb-6 flex flex-wrap items-center justify-between gap-3 border-b border-ink/10 pb-4">
            <div>
              <p className="text-xs uppercase tracking-[0.18em] text-ink/55">QuickTunnel</p>
              <h1 className="text-2xl font-semibold text-ink">Network Dashboard</h1>
            </div>

            <div className="flex items-center gap-3">
              <div className="rounded-lg bg-ink/5 px-3 py-2 text-sm">
                <p className="font-semibold text-ink">{user?.name || "User"}</p>
                <p className="text-xs text-ink/60">{user?.email || "-"}</p>
              </div>
              <button
                type="button"
                onClick={logout}
                className="inline-flex items-center gap-2 rounded-lg border border-ink/20 px-3 py-2 text-sm font-medium text-ink hover:bg-ink/10"
              >
                <LogOut size={16} />
                Sign out
              </button>
            </div>
          </header>

          <Outlet />
        </main>
      </div>
    </div>
  );
}
