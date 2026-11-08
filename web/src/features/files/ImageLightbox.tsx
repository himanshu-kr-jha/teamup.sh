import { useEffect } from 'react';
import { Download, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { downloadAttachment } from './download';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

export interface LightboxImage {
  objectKey: string;
  filename: string;
  /** A viewing URL, already presigned. */
  url: string;
}

/**
 * An image at full size, over the page.
 *
 * A thumbnail is enough to know a picture is there and not enough to read
 * anything in it, so it opens. Downloading is separate from viewing because
 * the two want different URLs: one is served inline, the other with a name and
 * a disposition that makes the browser save it.
 */
export function ImageLightbox({
  image,
  onClose,
}: {
  image: LightboxImage | null;
  onClose: () => void;
}) {
  const toast = useToast();

  // Escape closes it, and the page behind must not scroll while it is open.
  useEffect(() => {
    if (!image) return;
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
  }, [image, onClose]);

  if (!image) return null;

  const download = async () => {
    try {
      await downloadAttachment(image.objectKey, image.filename);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not download that file.'));
    }
  };

  return (
    <div
      className="fixed inset-0 z-[100] flex flex-col bg-black/90"
      role="dialog"
      aria-modal="true"
      aria-label={image.filename}
    >
      <div className="flex items-center gap-2 p-3">
        <span className="min-w-0 flex-1 truncate text-sm text-white/90">{image.filename}</span>
        <Button variant="secondary" size="sm" onClick={download}>
          <Download className="mr-2 h-4 w-4" />
          Download
        </Button>
        <Button variant="secondary" size="icon" onClick={onClose} aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </div>

      {/* Clicking the space around the picture closes it; clicking the picture
          itself does not, so a mis-aimed tap does not dismiss what you opened. */}
      <div
        className="flex min-h-0 flex-1 items-center justify-center p-4"
        onClick={onClose}
      >
        <img
          src={image.url}
          alt={image.filename}
          className="max-h-full max-w-full object-contain"
          onClick={(e) => e.stopPropagation()}
        />
      </div>
    </div>
  );
}
