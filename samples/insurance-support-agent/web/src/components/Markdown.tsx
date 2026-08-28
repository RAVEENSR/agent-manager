import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export function Markdown({ text }: { text: string }) {
  return (
    <div className="space-y-3 leading-relaxed [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: (p) => <h3 className="mt-4 text-sm font-semibold" {...p} />,
          h2: (p) => <h3 className="mt-4 text-sm font-semibold" {...p} />,
          h3: (p) => <h4 className="mt-4 text-sm font-semibold" {...p} />,
          h4: (p) => <h4 className="mt-4 text-sm font-semibold" {...p} />,
          p: (p) => <p className="whitespace-pre-wrap" {...p} />,
          strong: (p) => <strong className="font-semibold" {...p} />,
          ul: (p) => <ul className="list-disc space-y-1 pl-5" {...p} />,
          ol: (p) => <ol className="list-decimal space-y-1 pl-5" {...p} />,
          li: (p) => <li className="pl-0.5" {...p} />,
          a: (p) => <a className="underline" target="_blank" rel="noreferrer" {...p} />,
          code: (p) => (
            <code className="rounded border border-border-subtle bg-surface px-1 py-0.5 font-mono text-[0.85em]" {...p} />
          ),
          pre: (p) => (
            <pre
              className="overflow-x-auto rounded-lg border border-border-subtle bg-surface p-3 font-mono text-xs [&_code]:border-0 [&_code]:bg-transparent [&_code]:p-0"
              {...p}
            />
          ),
          blockquote: (p) => (
            <blockquote className="border-l-2 border-border-subtle pl-3 opacity-90" {...p} />
          ),
          hr: () => <hr className="border-border-subtle" />,
          table: (p) => (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left" {...p} />
            </div>
          ),
          th: (p) => <th className="border-b border-border-subtle py-1 pr-4 font-semibold" {...p} />,
          td: (p) => <td className="border-b border-border-subtle py-1 pr-4 align-top" {...p} />,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
