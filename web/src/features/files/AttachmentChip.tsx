import { useState } from 'react';
import { FileText, ImageIcon, Loader2 } from 'lucide-react';
import { filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

interface Attachment {
  id: string;
  objectKey: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * One stored file. Images preview inline; anything else downloads. The URL is
 * fetched on demand because it is presigned and short-lived.
 */
export function AttachmentChip({ attachment }: { attachment: Attachment }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState('');

  const isImage = attachment.contentType.startsWith('image/');

  const openFile = async () => {
    setBusy(true);
    try {
      const { data } = await filesApi.getDownloadUrl(attachment.objectKey, attachment.filename, isImage);
      if (isImage) setPreview(data.downloadUrl);
      else window.open(data.downloadUrl, '_blank', 'noopener');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not open that file.'));
    } finally {
      setBusy(false);
    }
  };

  if (preview) {
    return (
      <img
        src={preview}
        alt={attachment.filename}
        className="max-h-40 rounded-lg border border-border object-cover"
      />
    );
  }

  return (
    <button
      onClick={openFile}
      disabled={busy}
      className="flex items-center gap-2 rounded-lg border border-border px-3 py-2 text-left text-sm hover:bg-accent"
    >
      {busy ? (
        <Loader2 className="h-4 w-4 animate-spin" />
      ) : isImage ? (
        <ImageIcon className="h-4 w-4 text-muted-foreground" />
      ) : (
        <FileText className="h-4 w-4 text-muted-foreground" />
      )}
      <span className="max-w-40 truncate">{attachment.filename}</span>
      <span className="text-xs text-muted-foreground">{humanSize(attachment.sizeBytes)}</span>
    </button>
  );
}
