import { ReactNode, useEffect, useState } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStore } from "../store/authStore";

export default function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated, hydrate } = useAuthStore();
  const location = useLocation();
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => {
    hydrate();
    setHydrated(true);
  }, [hydrate]);

  // Don't redirect until hydration from localStorage is complete.
  // Without this, a page refresh always redirects to /login because
  // the initial Zustand state has isAuthenticated=false.
  if (!hydrated) {
    return null;
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }

  return <>{children}</>;
}
