import { CONFIG } from "@/lib/config";
import { useAuth } from "@/lib/auth";
import { AuthBadge } from "./AuthBadge";
import { Shield } from "./Shield";

export function ChatHeader({ onNewChat }: { onNewChat: () => void }) {
  const auth = useAuth();

  return (
    <header className="flex items-center gap-3 border-b border-border-subtle px-5 py-4">
      <div className="grid h-9 w-9 place-items-center rounded-full bg-brand text-white">
        <Shield className="h-5 w-5" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate font-semibold text-ink">{CONFIG.companyName} Support</div>
        <div className="text-xs text-muted">Policies and claims</div>
      </div>
      <AuthBadge />
      <button
        type="button"
        onClick={onNewChat}
        className="rounded-lg border border-border-subtle px-3 py-1.5 text-sm text-ink hover:bg-brand-soft"
      >
        New chat
      </button>
      {auth.mode !== "none" && auth.status === "signed-in" ? (
        <button
          type="button"
          onClick={auth.signOut}
          className="rounded-lg border border-border-subtle px-3 py-1.5 text-sm text-ink hover:bg-brand-soft"
        >
          Sign out
        </button>
      ) : null}
    </header>
  );
}
