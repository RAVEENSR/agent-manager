import type { Message } from "@/lib/types";
import { Markdown } from "./Markdown";
import { Shield } from "./Shield";

const TONE: Record<Message["role"], string> = {
  user: "bg-brand text-white",
  agent: "bg-bubble text-ink",
  error: "bg-bubble text-danger",
};

export function MessageBubble({ message }: { message: Message }) {
  const mine = message.role === "user";
  return (
    <div className={`flex gap-3 ${mine ? "flex-row-reverse" : ""}`}>
      <div
        className={`grid h-8 w-8 shrink-0 place-items-center rounded-full text-xs ${
          mine ? "bg-brand-soft text-ink" : "bg-brand text-white"
        }`}
      >
        {mine ? "You" : <Shield className="h-4 w-4" />}
      </div>
      <div className={`max-w-[80%] rounded-card px-4 py-3 text-sm ${TONE[message.role]}`}>
        {message.role === "agent" ? (
          <Markdown text={message.text} />
        ) : (
          <span className="whitespace-pre-wrap">{message.text}</span>
        )}
      </div>
    </div>
  );
}
