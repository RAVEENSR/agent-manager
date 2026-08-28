import { useEffect, useRef } from "react";

import type { Message } from "@/lib/types";
import { MessageBubble } from "./MessageBubble";
import { Shield } from "./Shield";
import { Suggestions } from "./Suggestions";

export function MessageList({
  messages,
  pending,
  onSuggestion,
}: {
  messages: Message[];
  pending: boolean;
  onSuggestion: (q: string) => void;
}) {
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [messages, pending]);

  return (
    <div className="flex-1 space-y-4 overflow-y-auto px-5 py-6">
      {messages.length === 0 ? (
        <div className="mx-auto max-w-xl space-y-4 py-10 text-center">
          <div className="mx-auto grid h-14 w-14 place-items-center rounded-full bg-brand text-white">
            <Shield className="h-7 w-7" />
          </div>
          <h2 className="text-lg font-semibold text-ink">How can we help today?</h2>
          <p className="text-sm text-muted">Ask about your cover, check a claim, or start a new one.</p>
          <Suggestions onPick={onSuggestion} />
        </div>
      ) : (
        messages.map((m) => <MessageBubble key={m.id} message={m} />)
      )}
      {pending ? <div className="pl-11 text-sm text-muted">…</div> : null}
      <div ref={end} />
    </div>
  );
}
