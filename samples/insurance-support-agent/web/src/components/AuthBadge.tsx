import { useAuth } from "@/lib/auth";

export function AuthBadge() {
  const auth = useAuth();

  if (auth.mode !== "none") return null;
  return (
    <span className="rounded-full bg-brand-soft px-2.5 py-1 text-xs text-muted">● no login</span>
  );
}
