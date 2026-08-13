import { useState } from 'react';
import { filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { humanSize } from './AttachmentChip';

export interface UploadedFile {
  /** The attachment row's id — what binds the file to a message when sent. */
  id: string;
  objectKey: string;
  filename: string;
  contentType: string;
  size: number;
}

// The server's cap, asked for once per page load. Fetching it is a courtesy so
// an over-sized file fails here instead of after the bytes have crossed the
// network; the enforcement is the content length signed into the upload URL,
// which object storage itself refuses to accept a different body for.
let uploadLimit: Promise<number> | null = null;

function maxUploadBytes(): Promise<number> {
  if (!uploadLimit) {
    uploadLimit = filesApi
      .limits()
      .then(({ data }) => data.maxUploadBytes)
      .catch(() => {
        // Do not block an upload because this one call failed; the server
        // still refuses anything too large. Try again next time.
        uploadLimit = null;
        return Number.POSITIVE_INFINITY;
      });
  }
  return uploadLimit;
}

/**
 * Uploading is three steps: ask for a presigned URL, PUT the bytes straight to
 * object storage, then tell the server it happened. That last step is what was
 * missing — without it the file was unreachable once the browser forgot the
 * object key.
 */
export function useUpload(category: string, refId: string) {
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState('');

  const upload = async (file: File): Promise<UploadedFile | null> => {
    setError('');
    setProgress(0);
    try {
      const limit = await maxUploadBytes();
      if (file.size > limit) {
        setError(
          `${file.name} is ${humanSize(file.size)}. The largest file this server accepts is ${humanSize(limit)}.`
        );
        return null;
      }

      const { data: presigned } = await filesApi.getUploadUrl({
        category,
        refId,
        filename: file.name,
        contentType: file.type || 'application/octet-stream',
        size: file.size,
      });

      await putWithProgress(presigned.uploadUrl, file, presigned.headers, setProgress);

      const { data: recorded } = await filesApi.record({
        category,
        refId,
        objectKey: presigned.objectKey,
        filename: file.name,
        contentType: file.type || 'application/octet-stream',
        size: file.size,
      });

      return {
        id: recorded.id,
        objectKey: presigned.objectKey,
        filename: file.name,
        contentType: file.type || 'application/octet-stream',
        // What storage says is there, not what we claimed on the way in.
        size: recorded.sizeBytes,
      };
    } catch (err) {
      setError(apiErrorMessage(err, 'Could not upload that file.'));
      return null;
    } finally {
      setProgress(null);
    }
  };

  return { upload, progress, error, clearError: () => setError('') };
}

// XHR rather than fetch: it is the only way to observe upload progress.
function putWithProgress(
  url: string,
  file: File,
  headers: Record<string, string> | undefined,
  onProgress: (pct: number) => void
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', url);
    for (const [k, v] of Object.entries(headers ?? {})) xhr.setRequestHeader(k, v);

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () =>
      xhr.status >= 200 && xhr.status < 300
        ? resolve()
        : reject(
            new Error(
              // 403 here is the signed content length being rejected: the file
              // is not the size the URL was issued for.
              xhr.status === 403
                ? 'Upload rejected: the file changed while it was being sent.'
                : `Upload failed (${xhr.status})`
            )
          );
    xhr.onerror = () => reject(new Error('Upload failed. Check your connection.'));
    xhr.send(file);
  });
}
