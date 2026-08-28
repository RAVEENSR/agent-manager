export function ConfigError({ message }: { message: string }) {
  return (
    <div className="grid h-full place-items-center p-6">
      <div className="max-w-md rounded-card border border-border-subtle bg-surface p-8">
        <h1 className="text-lg font-semibold text-danger">Configuration problem</h1>
        <p className="mt-3 text-sm text-ink">{message}</p>
      </div>
    </div>
  );
}
