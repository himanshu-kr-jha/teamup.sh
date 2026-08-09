import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Search, Loader2, Paperclip } from 'lucide-react';
import { Input } from '@/shared/components/ui/input';
import { messagesApi } from '@/shared/api/client';
import { formatRelativeTime, cn } from '@/shared/utils/cn';

interface Result {
  id: string;
  channelId: string;
  channelName: string;
  userName: string;
  content: string;
  highlight: string;
  createdAt: string;
}

// The server delimits matches with control characters rather than markup:
// Postgres does not escape the message it highlights, so returning HTML would
// hand user-authored markup straight to the browser. Rendered as React nodes,
// never as innerHTML.
const HIGHLIGHT_START = '\u0002';
const HIGHLIGHT_END = '\u0003';

function renderHighlight(snippet: string) {
  return snippet.split(HIGHLIGHT_START).flatMap((chunk, i) => {
    if (i === 0) return [<span key={`t${i}`}>{chunk}</span>];
    const [match, ...rest] = chunk.split(HIGHLIGHT_END);
    return [
      <mark key={`m${i}`} className="bg-yellow-200 text-inherit dark:bg-yellow-700">
        {match}
      </mark>,
      <span key={`t${i}`}>{rest.join(HIGHLIGHT_END)}</span>,
    ];
  });
}

/**
 * The header search box was decorative and the endpoint behind it did not
 * exist. Both now do: this searches messages the user is allowed to read.
 */
export function SearchBox() {
  const navigate = useNavigate();
  const [term, setTerm] = useState('');
  const [debounced, setDebounced] = useState('');
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Debounced so typing does not fire a query per keystroke.
  useEffect(() => {
    const t = setTimeout(() => setDebounced(term.trim()), 250);
    return () => clearTimeout(t);
  }, [term]);

  useEffect(() => {
    const onClickAway = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onClickAway);
    return () => document.removeEventListener('mousedown', onClickAway);
  }, []);

  const { data, isFetching } = useQuery({
    queryKey: ['search', debounced],
    queryFn: () => messagesApi.search({ q: debounced, limit: 12 }),
    enabled: debounced.length > 1,
  });
  // Files are matched by name from the same box: looking for something
  // somebody shared is the same act as looking for what they said about it.
  const { data: fileData, isFetching: fetchingFiles } = useQuery({
    queryKey: ['search-files', debounced],
    queryFn: () => messagesApi.searchFiles({ q: debounced, limit: 6 }),
    enabled: debounced.length > 1,
  });

  const results: Result[] = data?.data ?? [];
  const files = fileData?.data ?? [];
  const searching = isFetching || fetchingFiles;
  const nothing = results.length === 0 && files.length === 0;

  return (
    <div ref={containerRef} className="relative hidden md:block md:w-96">
      <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={term}
        onChange={(e) => {
          setTerm(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        placeholder="Search messages and files…"
        aria-label="Search messages and files"
        className="pl-10"
      />
      {searching && <Loader2 className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-muted-foreground" />}

      {open && debounced.length > 1 && (
        <div className="absolute left-0 right-0 top-full z-50 mt-1 max-h-96 overflow-y-auto rounded-lg border border-border bg-popover shadow-lg">
          {nothing && !searching && (
            <p className="p-4 text-sm text-muted-foreground">Nothing matches “{debounced}”.</p>
          )}

          {files.length > 0 && (
            <div className="border-b border-border">
              <p className="px-3 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Files
              </p>
              {files.map((f: any) => (
                <button
                  key={f.id}
                  onClick={() => {
                    setOpen(false);
                    setTerm('');
                    navigate(`/chat/${f.channelId}`);
                  }}
                  className="flex w-full items-center gap-2 p-3 text-left hover:bg-accent focus:bg-accent focus:outline-none"
                >
                  <Paperclip className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm">{f.filename}</span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {f.channelName || 'channel'} · {f.uploaderName} · {formatRelativeTime(f.createdAt)}
                    </span>
                  </span>
                </button>
              ))}
            </div>
          )}

          {results.length > 0 && files.length > 0 && (
            <p className="px-3 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Messages
            </p>
          )}
          {results.map((r) => (
            <button
              key={r.id}
              onClick={() => {
                setOpen(false);
                setTerm('');
                navigate(`/chat/${r.channelId}`);
              }}
              className={cn(
                'block w-full border-b border-border p-3 text-left last:border-0',
                'hover:bg-accent focus:bg-accent focus:outline-none'
              )}
            >
              <p className="text-xs text-muted-foreground">
                {r.channelName || 'channel'} · {r.userName} · {formatRelativeTime(r.createdAt)}
              </p>
              <p className="mt-0.5 text-sm">{renderHighlight(r.highlight || r.content)}</p>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
