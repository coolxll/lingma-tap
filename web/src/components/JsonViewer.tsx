import { useMemo, useState } from 'react';
import { Copy, Check } from 'lucide-react';

interface JsonViewerProps {
  data: string;
  maxHeight?: string;
}

export function JsonViewer({ data, maxHeight = '400px' }: JsonViewerProps) {
  const [copied, setCopied] = useState(false);

  const formatted = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(data), null, 2);
    } catch {
      return data;
    }
  }, [data]);

  const highlighted = useMemo(() => {
    return formatted
      .replace(/"([^"]+)":/g, '<span class="text-purple-400">"$1"</span>:')
      .replace(/: "([^"]*)"/g, ': <span class="text-green-400">"$1"</span>')
      .replace(/: (-?\d+\.?\d*)/g, ': <span class="text-blue-400">$1</span>')
      .replace(/: (true|false)/g, ': <span class="text-orange-400">$1</span>')
      .replace(/: (null)/g, ': <span class="text-zinc-500">$1</span>');
  }, [formatted]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(formatted);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  return (
    <div className="relative group w-full min-w-0">
      <button
        className="absolute top-2 right-2 p-1 rounded opacity-0 group-hover:opacity-100 transition-opacity z-10 hover:bg-zinc-700"
        onClick={handleCopy}
        title="Copy"
      >
        {copied ? (
          <Check className="w-3.5 h-3.5 text-green-400" />
        ) : (
          <Copy className="w-3.5 h-3.5 text-zinc-400" />
        )}
      </button>
      <div className="w-full overflow-x-auto">
        <pre
          className="p-3 bg-zinc-900 rounded text-xs font-mono whitespace-pre"
          style={{ maxHeight, minWidth: 'min-content' }}
          dangerouslySetInnerHTML={{ __html: highlighted }}
        />
      </div>
    </div>
  );
}
