import { useState } from "react";

export function Composer({
  disabled,
  onSend,
}: {
  disabled: boolean;
  onSend: (text: string) => void;
}) {
  const [text, setText] = useState("");

  return (
    <form
      className="flex gap-2 border-t border-border-subtle px-5 py-4"
      onSubmit={(e) => {
        e.preventDefault();
        const trimmed = text.trim();
        if (!trimmed || disabled) return;
        setText("");
        onSend(trimmed);
      }}
    >
      <input
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Ask about a policy or a claim"
        className="flex-1 rounded-card border border-border-subtle bg-surface px-4 py-3 text-sm text-ink outline-none focus:border-brand"
      />
      <button
        type="submit"
        disabled={disabled || !text.trim()}
        className="rounded-card bg-brand px-4 py-3 text-sm font-medium text-white disabled:opacity-50"
      >
        Send
      </button>
    </form>
  );
}
