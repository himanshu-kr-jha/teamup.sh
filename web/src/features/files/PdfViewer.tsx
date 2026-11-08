import { useCallback, useEffect, useRef, useState } from 'react';
import { Download, Loader2, Minus, Plus, X } from 'lucide-react';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import { Button } from '@/shared/components/ui/button';
import { filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { downloadAttachment } from './download';

export interface ViewedPdf {
  objectKey: string;
  filename: string;
}

/**
 * pdf.js, loaded the first time somebody opens a PDF rather than with the app:
 * it is several times the size of everything else in chat.
 */
async function loadPdfJs() {
  const [pdfjs, worker] = await Promise.all([
    import('pdfjs-dist'),
    import('pdfjs-dist/build/pdf.worker.min.mjs?url'),
  ]);
  pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
  return pdfjs;
}

const MIN_ZOOM = 0.5;
const MAX_ZOOM = 3;

/**
 * A PDF, read where it was sent, over the page.
 *
 * Drawn with pdf.js rather than handed to the browser in a frame: a phone's
 * browser shows nothing for a PDF in a frame, and offers to download it
 * instead, which is exactly the trip this saves. Pages are drawn as they come
 * into view, so a long document opens as fast as a short one.
 */
export function PdfViewer({ file, onClose }: { file: ViewedPdf | null; onClose: () => void }) {
  const toast = useToast();
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  const [failed, setFailed] = useState('');
  const [zoom, setZoom] = useState(1);
  const [width, setWidth] = useState(0);
  const scroller = useRef<HTMLDivElement>(null);

  // Escape closes it, and the page behind must not scroll while it is open.
  useEffect(() => {
    if (!file) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = previousOverflow;
    };
  }, [file, onClose]);

  useEffect(() => {
    if (!file) return;
    let cancelled = false;
    let opened: PDFDocumentProxy | null = null;
    setDoc(null);
    setFailed('');
    setZoom(1);
    (async () => {
      try {
        const [{ data }, pdfjs] = await Promise.all([
          filesApi.getDownloadUrl(file.objectKey, file.filename, true),
          loadPdfJs(),
        ]);
        opened = await pdfjs.getDocument({ url: data.downloadUrl }).promise;
        if (cancelled) {
          void opened.destroy();
          return;
        }
        setDoc(opened);
      } catch (err) {
        if (!cancelled) setFailed(apiErrorMessage(err, 'This PDF could not be opened.'));
      }
    })();
    return () => {
      cancelled = true;
      void opened?.destroy();
    };
  }, [file]);

  // Pages fit the width available, which changes when a phone turns round.
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () => setWidth(el.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [file]);

  const download = useCallback(async () => {
    if (!file) return;
    try {
      await downloadAttachment(file.objectKey, file.filename);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not download that file.'));
    }
  }, [file, toast]);

  if (!file) return null;

  const pageWidth = Math.max(0, Math.min(width - 32, 900)) * zoom;

  return (
    <div
      className="fixed inset-0 z-[100] flex flex-col bg-black/90"
      role="dialog"
      aria-modal="true"
      aria-label={file.filename}
    >
      <div className="flex items-center gap-2 p-3">
        <span className="min-w-0 flex-1 truncate text-sm text-white/90">
          {file.filename}
          {doc && (
            <span className="ml-2 text-white/60">
              {doc.numPages === 1 ? '1 page' : `${doc.numPages} pages`}
            </span>
          )}
        </span>
        <div className="hidden items-center gap-1 sm:flex">
          <Button
            variant="secondary"
            size="icon"
            onClick={() => setZoom((z) => Math.max(MIN_ZOOM, Math.round((z - 0.25) * 100) / 100))}
            disabled={!doc || zoom <= MIN_ZOOM}
            aria-label="Zoom out"
          >
            <Minus className="h-4 w-4" />
          </Button>
          <button
            type="button"
            onClick={() => setZoom(1)}
            className="w-14 text-center text-sm tabular-nums text-white/90"
            aria-label="Fit to width"
            title="Fit to width"
          >
            {Math.round(zoom * 100)}%
          </button>
          <Button
            variant="secondary"
            size="icon"
            onClick={() => setZoom((z) => Math.min(MAX_ZOOM, Math.round((z + 0.25) * 100) / 100))}
            disabled={!doc || zoom >= MAX_ZOOM}
            aria-label="Zoom in"
          >
            <Plus className="h-4 w-4" />
          </Button>
        </div>
        <Button variant="secondary" size="sm" onClick={download}>
          <Download className="h-4 w-4 sm:mr-2" />
          <span className="hidden sm:inline">Download</span>
          <span className="sr-only sm:hidden">Download</span>
        </Button>
        <Button variant="secondary" size="icon" onClick={onClose} aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div ref={scroller} className="min-h-0 flex-1 overflow-auto px-4 pb-6" data-testid="pdf-pages">
        {failed ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-center text-white/90">
            <p>{failed}</p>
            <Button variant="secondary" onClick={download}>
              <Download className="mr-2 h-4 w-4" />
              Download it instead
            </Button>
          </div>
        ) : !doc || pageWidth === 0 ? (
          <div className="flex h-full items-center justify-center">
            <Loader2 className="h-8 w-8 animate-spin text-white/80" aria-label="Opening the PDF" />
          </div>
        ) : (
          <div className="mx-auto flex w-max min-w-full flex-col items-center gap-4">
            {Array.from({ length: doc.numPages }, (_, i) => (
              <PdfPage key={i + 1} doc={doc} number={i + 1} width={pageWidth} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/** One page: a placeholder of the right shape until it scrolls near, then drawn. */
function PdfPage({ doc, number, width }: { doc: PDFDocumentProxy; number: number; width: number }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const holder = useRef<HTMLDivElement>(null);
  const [ratio, setRatio] = useState(1.414);
  const [visible, setVisible] = useState(number <= 2);
  const [drawn, setDrawn] = useState(false);

  useEffect(() => {
    const el = holder.current;
    if (!el || visible) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setVisible(true);
      },
      { rootMargin: '600px 0px' }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [visible]);

  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    let task: { cancel: () => void; promise: Promise<void> } | null = null;
    (async () => {
      const page = await doc.getPage(number);
      if (cancelled) return;
      const base = page.getViewport({ scale: 1 });
      setRatio(base.height / base.width);
      const scale = width / base.width;
      const dpr = window.devicePixelRatio || 1;
      const viewport = page.getViewport({ scale: scale * dpr });
      const el = canvas.current;
      if (!el) return;
      el.width = Math.floor(viewport.width);
      el.height = Math.floor(viewport.height);
      const context = el.getContext('2d');
      if (!context) return;
      task = page.render({ canvasContext: context, viewport });
      try {
        await task.promise;
        if (!cancelled) setDrawn(true);
      } catch {
        // Cancelled by a newer draw at another zoom.
      }
    })();
    return () => {
      cancelled = true;
      task?.cancel();
    };
  }, [doc, number, width, visible]);

  return (
    <div
      ref={holder}
      className="relative shrink-0 bg-white shadow-lg"
      style={{ width, height: width * ratio }}
      data-testid="pdf-page"
      data-drawn={drawn ? 'true' : 'false'}
      aria-label={`Page ${number}`}
    >
      <canvas ref={canvas} className="block h-full w-full" />
      {!drawn && (
        <div className="absolute inset-0 flex items-center justify-center">
          <Loader2 className="h-6 w-6 animate-spin text-slate-400" aria-hidden="true" />
        </div>
      )}
    </div>
  );
}
