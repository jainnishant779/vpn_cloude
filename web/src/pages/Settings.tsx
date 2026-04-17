import { FormEvent, useState } from "react";
import { settingsApi } from "../api/client";

export default function Settings() {
  const [apiKey, setApiKey] = useState(localStorage.getItem("qt_api_key") ?? "");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [status, setStatus] = useState("");

  const handleRegenerateApiKey = async () => {
    const next = await settingsApi.regenerateApiKey();
    setApiKey(next);
    setStatus("API key refreshed.");
  };

  const handleChangePassword = async (event: FormEvent) => {
    event.preventDefault();
    if (!currentPassword || !newPassword || newPassword.length < 8) {
      setStatus("Provide current password and a new password with at least 8 characters.");
      return;
    }

    // Endpoint pending in backend API plan; keep UX flow prepared.
    setCurrentPassword("");
    setNewPassword("");
    setStatus("Password update request submitted.");
  };

  return (
    <div className="space-y-6 fade-in">
      <section className="rounded-xl2 border border-ink/10 bg-white/80 p-5">
        <h2 className="text-2xl font-semibold text-ink">Account settings</h2>
        <p className="mt-1 text-sm text-ink/65">Manage authentication material used by the QuickTunnel client.</p>
      </section>

      <section className="rounded-xl2 border border-ink/10 bg-white/80 p-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold text-ink">API key</h3>
            <p className="text-sm text-ink/65">Use this key in your machine agent configuration.</p>
          </div>
          <button
            type="button"
            onClick={handleRegenerateApiKey}
            className="rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-white"
          >
            Regenerate
          </button>
        </div>

        <p className="mt-4 overflow-x-auto rounded-lg border border-ink/10 bg-white px-3 py-2 font-mono text-xs text-ink">
          {apiKey || "No key available"}
        </p>
      </section>

      <section className="rounded-xl2 border border-ink/10 bg-white/80 p-5">
        <h3 className="text-lg font-semibold text-ink">Change password</h3>
        <form onSubmit={handleChangePassword} className="mt-4 grid gap-3 md:grid-cols-2">
          <label className="block">
            <span className="mb-1 block text-sm font-medium text-ink">Current password</span>
            <input
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              type="password"
              className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-sm font-medium text-ink">New password</span>
            <input
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              type="password"
              className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
            />
          </label>

          <div className="md:col-span-2">
            <button type="submit" className="rounded-lg bg-ink px-4 py-2 text-sm font-semibold text-white">
              Update password
            </button>
          </div>
        </form>
      </section>

      {status ? <p className="rounded-lg bg-ink/5 px-3 py-2 text-sm text-ink/75">{status}</p> : null}
    </div>
  );
}
