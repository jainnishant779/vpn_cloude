import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import { useAuthStore } from "../store/authStore";

export default function Register() {
  const navigate = useNavigate();
  const { register, loading, error } = useAuthStore();

  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [formError, setFormError] = useState("");

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setFormError("");

    if (!name.trim()) {
      setFormError("Name is required.");
      return;
    }
    if (!email.includes("@")) {
      setFormError("Please enter a valid email address.");
      return;
    }
    if (password.length < 8) {
      setFormError("Password must be at least 8 characters.");
      return;
    }
    if (password !== confirmPassword) {
      setFormError("Passwords do not match.");
      return;
    }

    try {
      await register(name.trim(), email.trim(), password);
      navigate("/dashboard", { replace: true });
    } catch {
      // Store handles error text.
    }
  };

  return (
    <div className="mx-auto flex min-h-screen max-w-5xl items-center px-4 py-10">
      <div className="grid w-full gap-6 rounded-xl2 border border-ink/10 bg-white/90 p-6 shadow-card md:grid-cols-2 md:p-8">
        <section className="rounded-xl2 bg-ember p-6 text-white">
          <p className="text-xs uppercase tracking-[0.2em] text-white/70">QuickTunnel</p>
          <h1 className="mt-2 text-3xl font-semibold">Create account</h1>
          <p className="mt-4 text-sm text-white/80">
            Build your private remote access mesh in minutes and onboard peers with zero inbound firewall rules.
          </p>
        </section>

        <section className="fade-in">
          <h2 className="text-2xl font-semibold text-ink">Get started</h2>
          <p className="mt-1 text-sm text-ink/60">Register once and use the API key in your client agent.</p>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <label className="block">
              <span className="mb-1 block text-sm font-medium text-ink">Name</span>
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                type="text"
                className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
                placeholder="Nishant"
              />
            </label>

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

            <label className="block">
              <span className="mb-1 block text-sm font-medium text-ink">Confirm Password</span>
              <input
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
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
              {loading ? <LoadingSpinner label="Creating account" /> : "Create account"}
            </button>
          </form>

          <p className="mt-4 text-sm text-ink/65">
            Already have an account?{" "}
            <Link to="/login" className="font-semibold text-accent hover:underline">
              Sign in
            </Link>
          </p>
        </section>
      </div>
    </div>
  );
}
