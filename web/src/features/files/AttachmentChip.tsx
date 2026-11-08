import { useEffect, useState } from 'react';
import { Download, FileText, ImageIcon, Loader2 } from 'lucide-react';
import { filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { ImageLightbox, type LightboxImage } from './ImageLightbox';
import { PdfViewer, type ViewedPdf } from './PdfViewer';
import { downloadAttachment, isPdf as isPdfFile } from './download';
import { VoicePlayer } from './VoicePlayer';
import { VideoNotePlayer } from './VideoNotePlayer';

interface Attachment {
  id: string;
  objectKey: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
}

export function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * One stored file.
 *
 * A picture shows itself, because a filename tells you nothing about what is in
 * it, and opens full size when clicked. A PDF opens to be read where it was
 * sent. Anything else is a row you can download, and pictures and PDFs have a
 * download button of their own. URLs are fetched when needed rather than up front: they are
 * presigned and expire.
 */
export function AttachmentChip({ attachment }: { attachment: Attachment }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [thumbnail, setThumbnail] = useState('');
  const [enlarged, setEnlarged] = useState<LightboxImage | null>(null);
  const [reading, setReading] = useState<ViewedPdf | null>(null);

  const isImage = attachment.contentType.startsWith('image/');
  const isAudio = attachment.contentType.startsWith('audio/');
  const isVideo = attachment.contentType.startsWith('video/');
  // Played where it was sent, like a picture is shown: voice and video messages
  // are the point, not a file to fetch.
  const playable = isAudio || isVideo;
  const isPdf = !isImage && !playable && isPdfFile(attachment);

  const download = async () => {
    try {
      await downloadAttachment(attachment.objectKey, attachment.filename);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not download that file.'));
    }
  };

  // A picture is fetched as soon as it is on screen: showing a button that
  // says "image.png" and making somebody click it to find out what it is
  // wastes the one thing a picture is good at.
  useEffect(() => {
    if (!(isImage || playable) || thumbnail) return;
    let cancelled = false;
    (async () => {
      try {
        const { data } = await filesApi.getDownloadUrl(
          attachment.objectKey,
          attachment.filename,
          true
        );
        if (!cancelled) setThumbnail(data.downloadUrl);
      } catch {
        // Falls back to the row below, which can still be downloaded.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isImage, playable, thumbnail, attachment.objectKey, attachment.filename]);

  const open = async () => {
    // Read where it was sent, rather than downloaded to be read somewhere else.
    if (isPdf) {
      setReading({ objectKey: attachment.objectKey, filename: attachment.filename });
      return;
    }
    setBusy(true);
    try {
      const { data } = await filesApi.getDownloadUrl(
        attachment.objectKey,
        attachment.filename,
        isImage
      );
      if (isImage) {
        setEnlarged({
          objectKey: attachment.objectKey,
          filename: attachment.filename,
          url: data.downloadUrl,
        });
      } else {
        window.open(data.downloadUrl, '_blank', 'noopener');
      }
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not open that file.'));
    } finally {
      setBusy(false);
    }
  };

  if (playable && thumbnail) {
    return isAudio ? (
      <VoicePlayer src={thumbnail} id={attachment.objectKey} filename={attachment.filename} onDownload={download} />
    ) : (
      <VideoNotePlayer src={thumbnail} filename={attachment.filename} onDownload={download} />
    );
  }

  // Waiting for its address, a voice or video message keeps its shape.
  if (playable && !thumbnail) {
    return isAudio ? (
      <span className="flex h-[3.25rem] w-[17rem] max-w-full items-center gap-2.5 rounded-2xl border border-border/70 bg-background px-2 shadow-sm" aria-hidden="true">
        <span className="h-9 w-9 shrink-0 animate-pulse rounded-full bg-muted" />
        <span className="h-3 flex-1 animate-pulse rounded-full bg-muted" />
      </span>
    ) : (
      <span className="block aspect-square w-60 max-w-full animate-pulse rounded-2xl border border-border/70 bg-muted" aria-hidden="true" />
    );
  }

  if (isImage && thumbnail) {
    return (
      <span className="group/attachment relative inline-block max-w-full">
        <button
          onClick={open}
          className="group/image relative overflow-hidden rounded-lg border border-border"
          aria-label={`Open ${attachment.filename}`}
        >
          <img
            src={thumbnail}
            alt={attachment.filename}
            className="max-h-48 min-h-24 min-w-24 max-w-full bg-muted object-contain"
          />
          <span className="pointer-events-none absolute inset-0 flex items-center justify-center bg-black/0 transition-colors group-hover/image:bg-black/20" />
        </button>
        {/* Saving it without opening it first, as for any other file. */}
        <button
          type="button"
          onClick={download}
          aria-label={`Download ${attachment.filename}`}
          title="Download"
          className="absolute right-1.5 top-1.5 flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white opacity-0 transition-opacity hover:bg-black/75 focus:opacity-100 group-hover/attachment:opacity-100 [@media(hover:none)]:opacity-100"
        >
          <Download className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
        <ImageLightbox image={enlarged} onClose={() => setEnlarged(null)} />
      </span>
    );
  }

  const row = (
    <button
      onClick={open}
      disabled={busy}
      className={
        isPdf
          ? 'flex min-w-0 items-center gap-2 py-2 pl-3 pr-2 text-left text-sm hover:bg-accent'
          : 'flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-left text-sm text-foreground hover:bg-accent'
      }
      aria-label={isPdf ? `Open ${attachment.filename}` : undefined}
    >
      {busy ? (
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
      ) : isImage ? (
        <ImageIcon className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
      ) : (
        <FileText className={isPdf ? 'h-4 w-4 text-red-500' : 'h-4 w-4 text-muted-foreground'} aria-hidden="true" />
      )}
      <span className="max-w-40 truncate">{attachment.filename}</span>
      <span className="shrink-0 text-xs text-muted-foreground">{humanSize(attachment.sizeBytes)}</span>
      {!isPdf && <Download className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />}
    </button>
  );

  if (isPdf) {
    return (
      <>
        <span className="flex max-w-full items-stretch overflow-hidden rounded-lg border border-border bg-background text-foreground">
          {row}
          <button
            type="button"
            onClick={download}
            aria-label={`Download ${attachment.filename}`}
            title="Download"
            className="flex items-center border-l border-border px-2.5 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <Download className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        </span>
        <PdfViewer file={reading} onClose={() => setReading(null)} />
      </>
    );
  }

  return (
    <>
      {row}
      <ImageLightbox image={enlarged} onClose={() => setEnlarged(null)} />
    </>
  );
}
