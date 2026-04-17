import { FormEvent, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useAuthStore } from "../store/authStore";
import LoadingSpinner from "../components/LoadingSpinner";

export default function Login() {
  const navigate = useNavigate();
  const location = useLocation();
  const { login, loading, error } = useAuthStore();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [formError, setFormError] = useState("");

  const redirectPath = (location.state as { from?: string } | null)?.from ?? "/dashboard";

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setFormError("");

    if (!email.includes("@")) {
      setFormError("Please enter a valid email address.");
      return;
    }
    if (password.length < 8) {
      setFormError("Password must be at least 8 characters.");
      return;
    }

    try {
      await login(email, password);
      navigate(redirectPath, { replace: true });
    } catch {
      // Store handles the error message.
    }
  };

  return (
    <div className="mx-auto flex min-h-screen max-w-5xl items-center px-4 py-10">
      <div className="grid w-full gap-6 rounded-xl2 border border-ink/10 bg-white/90 p-6 shadow-card md:grid-cols-2 md:p-8">
        <section className="rounded-xl2 bg-accent p-6 text-white">
          <p className="text-xs uppercase tracking-[0.2em] text-white/70">QuickTunnel</p>
          <h1 className="mt-2 text-3xl font-semibold">Welcome back</h1>
          <p className="mt-4 text-sm text-white/80">
            Securely connect devices, monitor peers, and launch remote desktops through your private mesh.
          </p>
        </section>

        <section className="fade-in">
          <h2 className="text-2xl font-semibold text-ink">Sign in</h2>
          <p className="mt-1 text-sm text-ink/60">Use your QuickTunnel account credentials.</p>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <label className="block">
              <span className="mb-1 block text-sm font-medium text-ink">Email</span>
              <input
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                type="email"
                className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
                placeholder="you@example.com"
              />
            </label>

            <label className="block">
              <span className="mb-1 block text-sm font-medium text-ink">Password</span>
              <input
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                type="password"
                className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
                placeholder="••••••••"
              />
            </label>

            {(formError || error) && (
              <p className="rounded-lg border border-ember/20 bg-ember/10 px-3 py-2 text-sm text-ember">{formError || error}</p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-lg bg-ink px-3 py-2 font-semibold text-white disabled:opacity-60"
            >
              {loading ? <LoadingSpinner label="Signing in" /> : "Sign in"}
            </button>
          </form>

          <p className="mt-4 text-sm text-ink/65">
            New to QuickTunnel?{" "}
            <Link to="/register" className="font-semibold text-accent hover:underline">
              Create account
            </Link>
          </p>
        </section>
      </div>
    </div>
  );
}
