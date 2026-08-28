const SUGGESTIONS = [
  { q: "What can you do?", hint: "See everything this agent handles" },
  { q: "What policies do I have?", hint: "Cover, premiums and renewal dates" },
  { q: "What is happening with my claims?", hint: "Status and next steps" },
  { q: "What am I covered for on OZ-AUTO-4417?", hint: "Full cover on one policy" },
];

export function Suggestions({ onPick }: { onPick: (q: string) => void }) {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {SUGGESTIONS.map((s) => (
        <button
          key={s.q}
          type="button"
          onClick={() => onPick(s.q)}
          className="rounded-card border border-border-subtle p-3 text-left hover:bg-brand-soft"
        >
          <div className="text-sm font-medium text-ink">{s.q}</div>
          <div className="text-xs text-muted">{s.hint}</div>
        </button>
      ))}
    </div>
  );
}
