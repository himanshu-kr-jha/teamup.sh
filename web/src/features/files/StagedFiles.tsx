import { useEffect, useMemo, useState } from 'react';
import { ChevronLeft, ChevronRight, File as FileIcon, FileAudio, FileText, Loader2, Play, Trash2, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { cn } from '@/shared/utils/cn';
import { humanSize } from './AttachmentChip';
import { isPdf } from './download';
import { PdfViewer, type ViewedPdf } from './PdfViewer';
import type { UploadedFile } from './useUpload';

/** An uploaded file waiting to be sent, with the copy it was picked from, to preview without a round trip. */
export type StagedFile = UploadedFile & { local?: File };

type Kind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'other';

const TEXT_EXTENSIONS = new Set([
  'txt', 'md', 'csv', 'tsv', 'json', 'log', 'xml', 'yml', 'yaml', 'ini', 'toml', 'sql',
  'js', 'ts', 'tsx', 'jsx', 'go', 'py', 'java', 'rb', 'rs', 'c', 'h', 'cpp', 'cs', 'sh', 'html', 'css',
]);

const extensionOf = (name: string) => (name.includes('.') ? name.split('.').pop()!.toLowerCase() : '');

export function kindOf(file: { filename: string; contentType: string }): Kind {
  const type = file.contentType || '';
  if (type.startsWith('image/')) return 'image';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('audio/')) return 'audio';
  if (isPdf(file)) return 'pdf';
  if (type.startsWith('text/') || /^application\/([\w.-]+\+)?(json|xml|x?-?yaml|javascript|x-sh)$/.test(type) || TEXT_EXTENSIONS.has(extensionOf(file.filename))) {
    return 'text';
  }
  return 'other';
}

/** A browser address for the picked copy, for as long as the component showing it is up. */
function useLocalUrl(file: File | undefined) {
  const url = useMemo(() => (file ? URL.createObjectURL(file) : ''), [file]);
  useEffect(() => () => {
    if (url) URL.revokeObjectURL(url);
  }, [url]);
  return url;
}

/**
 * What is about to be sent, before it is.
 *
 * Each file is a tile that opens to show what is in it: a picture, a video or
 * a recording plays, a PDF reads, and a text file shows its text. Sending the
 * wrong photo to a channel cannot be taken back from everyone's notifications,
 * so looking first is worth a click.
 */
export function StagedFiles({
  files,
  onRemove,
  className,
  sendWord = 'Send',
}: {
  files: StagedFile[];
  onRemove: (file: StagedFile) => void;
  className?: string;
  /** What the button that sends them says. */
  sendWord?: string;
}) {
  const [open, setOpen] = useState<number | null>(null);
  const [reading, setReading] = useState<ViewedPdf | null>(null);
  // A file removed while it was open: show its neighbour, or close.
  const shown = open === null ? null : files[Math.min(open, files.length - 1)] ?? null;

  const preview = (index: number) => {
    const file = files[index];
    if (kindOf(file) === 'pdf') setReading({ objectKey: file.objectKey, filename: file.filename });
    else setOpen(index);
  };

  if (files.length === 0) return null;

  return (
    <div className={className}>
      <p className="mb-2 text-xs font-medium text-muted-foreground">
        {files.length === 1 ? 'Attached' : `${files.length} attached`}. Press {sendWord} to share.{' '}
        <span className="font-normal">Click {files.length === 1 ? 'it' : 'one'} to see it first.</span>
      </p>
      <ul className="flex gap-3 overflow-x-auto pb-1 pt-1.5" aria-label="Files to send">
        {files.map((file, index) => (
          <StagedTile key={file.id} file={file} onOpen={() => preview(index)} onRemove={() => onRemove(file)} />
        ))}
      </ul>
      {shown && (
        <StagedPreview
          file={shown}
          position={files.indexOf(shown)}
          count={files.length}
          onStep={(step) => {
            const next = (files.indexOf(shown) + step + files.length) % files.length;
            if (kindOf(files[next]) === 'pdf') {
              setOpen(null);
              preview(next);
            } else setOpen(next);
          }}
          onRemove={() => {
            const at = files.indexOf(shown);
            onRemove(shown);
            if (files.length <= 1) setOpen(null);
            else setOpen(Math.min(at, files.length - 2));
          }}
          onClose={() => setOpen(null)}
        />
      )}
      <PdfViewer file={reading} onClose={() => setReading(null)} />
    </div>
  );
}

function StagedTile({ file, onOpen, onRemove }: { file: StagedFile; onOpen: () => void; onRemove: () => void }) {
  const kind = kindOf(file);
  const url = useLocalUrl(kind === 'image' || kind === 'video' ? file.local : undefined);
  const ext = extensionOf(file.filename);

  return (
    <li className="relative w-20 shrink-0">
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Preview ${file.filename}`}
        title={`${file.filename} · ${humanSize(file.size)}`}
        className="relative flex h-20 w-20 flex-col items-center justify-center gap-1 overflow-hidden rounded-lg border border-border bg-background text-muted-foreground transition-colors hover:border-primary/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {kind === 'image' && url ? (
          <img src={url} alt="" className="h-full w-full object-cover" />
        ) : kind === 'video' && url ? (
          <>
            <video src={url} muted preload="metadata" className="h-full w-full object-cover" />
            <span className="absolute flex h-7 w-7 items-center justify-center rounded-full bg-black/55 text-white">
              <Play className="ml-0.5 h-3.5 w-3.5" fill="currentColor" aria-hidden="true" />
            </span>
          </>
        ) : (
          <>
            {kind === 'audio' ? (
              <FileAudio className="h-6 w-6" aria-hidden="true" />
            ) : kind === 'pdf' ? (
              <FileText className="h-6 w-6 text-red-500" aria-hidden="true" />
            ) : kind === 'text' ? (
              <FileText className="h-6 w-6" aria-hidden="true" />
            ) : (
              <FileIcon className="h-6 w-6" aria-hidden="true" />
            )}
            {ext && <span className="max-w-full truncate px-1 text-[10px] font-semibold uppercase tracking-wide">{ext}</span>}
          </>
        )}
      </button>
      <p className="mt-1 truncate text-[11px] leading-tight text-foreground" title={file.filename}>
        {file.filename}
      </p>
      <p className="text-[10px] leading-tight text-muted-foreground">{humanSize(file.size)}</p>
      <button
        type="button"
        onClick={onRemove}
        aria-label={`Remove ${file.filename}`}
        className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full border border-border bg-background text-muted-foreground shadow-sm hover:bg-accent hover:text-foreground"
      >
        <X className="h-3 w-3" aria-hidden="true" />
      </button>
    </li>
  );
}

/** Text is shown up to this much; a log of many megabytes is still a log. */
const TEXT_LIMIT = 200_000;

function StagedPreview({
  file,
  position,
  count,
  onStep,
  onRemove,
  onClose,
}: {
  file: StagedFile;
  position: number;
  count: number;
  onStep: (step: 1 | -1) => void;
  onRemove: () => void;
  onClose: () => void;
}) {
  const kind = kindOf(file);
  const url = useLocalUrl(kind === 'image' || kind === 'video' || kind === 'audio' ? file.local : undefined);
  const [text, setText] = useState<{ body: string; cut: boolean } | null>(null);

  useEffect(() => {
    setText(null);
    if (kind !== 'text' || !file.local) return;
    let cancelled = false;
    file.local
      .slice(0, TEXT_LIMIT)
      .text()
      .then((body) => {
        if (!cancelled) setText({ body, cut: file.local!.size > TEXT_LIMIT });
      })
      .catch(() => {
        if (!cancelled) setText({ body: '', cut: false });
      });
    return () => {
      cancelled = true;
    };
  }, [file, kind]);

  // Escape closes it, the arrows step through what is attached, and the page
  // behind stays where it was.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      } else if (count > 1 && e.key === 'ArrowRight') onStep(1);
      else if (count > 1 && e.key === 'ArrowLeft') onStep(-1);
    };
    document.addEventListener('keydown', onKey);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = previousOverflow;
    };
  }, [count, onClose, onStep]);

  const preview = (() => {
    if (!file.local) {
      return <Nothing file={file} why="This one can only be seen once it is sent." />;
    }
    switch (kind) {
      case 'image':
        return <img src={url} alt={file.filename} className="max-h-full max-w-full object-contain" onClick={(e) => e.stopPropagation()} />;
      case 'video':
        return (
          <video src={url} controls autoPlay className="max-h-full max-w-full rounded-lg bg-black" onClick={(e) => e.stopPropagation()} />
        );
      case 'audio':
        return (
          <div className="w-full max-w-md rounded-xl bg-white/10 p-6 text-center" onClick={(e) => e.stopPropagation()}>
            <FileAudio className="mx-auto mb-4 h-10 w-10 text-white/80" aria-hidden="true" />
            <audio src={url} controls autoPlay className="w-full" />
          </div>
        );
      case 'text':
        return text === null ? (
          <Loader2 className="h-6 w-6 animate-spin text-white/80" aria-label="Loading" />
        ) : (
          <div
            className="flex max-h-full w-full max-w-3xl flex-col overflow-hidden rounded-lg bg-background text-foreground shadow-xl"
            onClick={(e) => e.stopPropagation()}
          >
            <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-relaxed">
              {text.body || 'This file is empty.'}
            </pre>
            {text.cut && (
              <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
                Showing the start. The whole file is sent.
              </p>
            )}
          </div>
        );
      default:
        return <Nothing file={file} why={`There is no preview for ${extensionOf(file.filename) ? `a .${extensionOf(file.filename)} file` : 'this kind of file'}. It will be sent as a file people can download.`} />;
    }
  })();

  return (
    <div className="fixed inset-0 z-[100] flex flex-col bg-black/90" role="dialog" aria-modal="true" aria-label={`Preview of ${file.filename}`}>
      <div className="flex items-center gap-2 p-3">
        <span className="min-w-0 flex-1 truncate text-sm text-white/90">
          {file.filename}
          <span className="ml-2 text-white/50">{humanSize(file.size)}</span>
          {count > 1 && <span className="ml-2 text-white/50">{position + 1} of {count}</span>}
        </span>
        <Button variant="secondary" size="sm" onClick={onRemove}>
          <Trash2 className="mr-2 h-4 w-4" />
          Remove
        </Button>
        <Button variant="secondary" size="icon" onClick={onClose} aria-label="Close preview">
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div className="relative flex min-h-0 flex-1 items-center justify-center p-4" onClick={onClose}>
        {preview}
        {count > 1 && (
          <>
            <StepButton side="left" onClick={() => onStep(-1)} />
            <StepButton side="right" onClick={() => onStep(1)} />
          </>
        )}
      </div>
    </div>
  );
}

function StepButton({ side, onClick }: { side: 'left' | 'right'; onClick: () => void }) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      aria-label={side === 'left' ? 'Previous file' : 'Next file'}
      className={cn(
        'absolute top-1/2 flex h-10 w-10 -translate-y-1/2 items-center justify-center rounded-full bg-white/15 text-white hover:bg-white/25',
        side === 'left' ? 'left-3' : 'right-3'
      )}
    >
      <Icon className="h-5 w-5" aria-hidden="true" />
    </button>
  );
}

function Nothing({ file, why }: { file: StagedFile; why: string }) {
  return (
    <div className="max-w-sm rounded-xl bg-background p-6 text-center text-foreground shadow-xl" onClick={(e) => e.stopPropagation()}>
      <FileIcon className="mx-auto mb-3 h-10 w-10 text-muted-foreground" aria-hidden="true" />
      <p className="truncate font-medium">{file.filename}</p>
      <p className="mb-3 text-xs text-muted-foreground">{humanSize(file.size)}</p>
      <p className="text-sm text-muted-foreground">{why}</p>
    </div>
  );
}
