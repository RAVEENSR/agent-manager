import { CONFIG } from "@/lib/config";
import { useAuth } from "@/lib/auth";
import { Shield } from "./Shield";

export function LoginScreen() {
  const auth = useAuth();

  return (
    <div className="grid h-full place-items-center p-6">
      <div className="w-full max-w-sm rounded-card border border-border-subtle bg-surface p-8 text-center">
        <div className="mx-auto mb-4 grid h-14 w-14 place-items-center rounded-full bg-brand text-white">
          <Shield className="h-7 w-7" />
        </div>
        <h1 className="text-xl font-bold text-ink">{CONFIG.companyName} Support</h1>
        <p className="mb-6 mt-2 text-sm text-muted">
          Sign in to talk with an insurance agent. Check your cover, track a claim, or open a
          new one.
        </p>
        <button
          type="button"
          onClick={auth.signIn}
          className="w-full rounded-card bg-brand px-4 py-3 text-sm font-medium text-white"
        >
          Sign in
        </button>
        {auth.error ? <p className="mt-4 text-sm text-danger">{auth.error}</p> : null}
      </div>
    </div>
  );
}
