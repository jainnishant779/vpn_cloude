import { ReactNode, useEffect } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStore } from "../store/authStore";

export default function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated, hydrate } = useAuthStore();
  const location = useLocation();

  useEffect(() => {
    hydrate();
  }, [hydrate]);

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }

  return <>{children}</>;
}
