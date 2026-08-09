import { useState } from 'react';
import { filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';

export interface UploadedFile {
  /** The attachment row's id — what binds the file to a message when sent. */
  id: string;
  objectKey: string;
  filename: string;
  contentType: string;
  size: number;
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
        size: file.size,
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
        : reject(new Error(`Upload failed (${xhr.status})`));
    xhr.onerror = () => reject(new Error('Upload failed. Check your connection.'));
    xhr.send(file);
  });
}
