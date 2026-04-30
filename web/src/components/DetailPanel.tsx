import { memo } from 'react';
import { TrafficRecord, formatTimestamp, getEndpointLabel } from '@/lib/types';
import { JsonViewer } from './JsonViewer';
import { SseEventList } from './SseEventList';

interface DetailPanelProps {
  request: TrafficRecord | null;
  response: TrafficRecord | null;
}

export const DetailPanel = memo(function DetailPanel({ request, response }: DetailPanelProps) {
  if (!request) {
    return (
      <div className="h-full flex items-center justify-center text-zinc-600 text-sm">
        Select a record to view details
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto bg-zinc-950">
      {/* Summary */}
      <div className="px-4 py-3 border-b border-zinc-800">
        <div className="flex items-center gap-2 mb-2">
          <span className="px-2 py-0.5 rounded text-xs font-medium bg-blue-900/50 text-blue-400">
            {request.method}
          </span>
          <span className="text-xs text-zinc-300 font-mono break-all">{request.url}</span>
          <span className="text-xs text-zinc-600 ml-auto">#{request.index}</span>
        </div>
        <div className="flex items-center gap-4 text-xs text-zinc-500">
          <span>{formatTimestamp(request.ts)}</span>
          <span className="text-zinc-600">|</span>
          <span>{getEndpointLabel(request.endpoint_type)}</span>
          {request.is_encoded && (
            <>
              <span className="text-zinc-600">|</span>
              <span className="text-amber-400">Encoded</span>
            </>
          )}
          {response && response.status > 0 && (
            <>
              <span className="text-zinc-600">|</span>
              <span className={response.status < 400 ? 'text-green-400' : 'text-red-400'}>
                {response.status} {response.status_text}
              </span>
            </>
          )}
          <span className="text-zinc-600">|</span>
          <span>Session: {request.session}</span>
        </div>
      </div>

      {/* Request Headers */}
      {request.request_headers && Object.keys(request.request_headers).length > 0 && (
        <Section title="Request Headers">
          <HeadersTable headers={request.request_headers} />
        </Section>
      )}

      {/* Request Body */}
      <Section title={`Request Body${request.is_encoded ? ' (decoded)' : ''}`}>
        {request.request_body ? (
          <JsonViewer data={request.request_body} maxHeight="500px" />
        ) : (
          <p className="text-zinc-600 text-xs">Empty body</p>
        )}
      </Section>

      {/* Raw encoded body (if different from decoded) */}
      {request.is_encoded && request.request_body_raw && request.request_body_raw !== request.request_body && (
        <Section title="Request Body (raw encoded)">
          <pre className="p-3 bg-zinc-900 rounded text-xs font-mono text-zinc-400 whitespace-pre-wrap break-all max-h-[200px] overflow-y-auto">
            {request.request_body_raw.slice(0, 2000)}
            {request.request_body_raw.length > 2000 ? '...' : ''}
          </pre>
        </Section>
      )}

      {/* Response section */}
      {response ? (
        <>
          {/* Response Headers */}
          {response.response_headers && Object.keys(response.response_headers).length > 0 && (
            <Section title="Response Headers">
              <HeadersTable headers={response.response_headers} />
            </Section>
          )}

          {/* Response Body */}
          <Section title="Response Body">
            {response.is_sse && response.sse_events && response.sse_events.length > 0 ? (
              <SseEventList events={response.sse_events} />
            ) : response.response_body ? (
              <JsonViewer data={response.response_body} maxHeight="500px" />
            ) : (
              <p className="text-zinc-600 text-xs">Empty body</p>
            )}
          </Section>
        </>
      ) : (
        <Section title="Response">
          <p className="text-zinc-600 text-xs">Waiting for response...</p>
        </Section>
      )}
    </div>
  );
});

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border-b border-zinc-800">
      <div className="px-4 py-2">
        <h3 className="text-xs font-medium text-zinc-400 mb-2">{title}</h3>
        {children}
      </div>
    </div>
  );
}

function HeadersTable({ headers }: { headers: Record<string, string> }) {
  const entries = Object.entries(headers);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <tbody>
          {entries.map(([key, value]) => (
            <tr key={key} className="border-b border-zinc-900">
              <td className="py-1 pr-3 text-zinc-400 font-medium whitespace-nowrap align-top">{key}</td>
              <td className="py-1 text-zinc-300 font-mono break-all">{value}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
