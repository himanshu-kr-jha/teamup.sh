import { filesApi } from '@/shared/api/client';

/**
 * Saves a stored file under its own name.
 *
 * Asked for without the inline flag, so the browser is told to save it rather
 * than display it. Viewing and saving want different URLs for that reason.
 */
export async function downloadAttachment(objectKey: string, filename: string) {
  const { data } = await filesApi.getDownloadUrl(objectKey, filename, false);
  const link = document.createElement('a');
  link.href = data.downloadUrl;
  link.download = filename;
  link.rel = 'noopener';
  document.body.appendChild(link);
  link.click();
  link.remove();
}

/** Whether a file opens in the PDF viewer rather than downloading. */
export function isPdf(file: { contentType?: string; filename: string }): boolean {
  return file.contentType === 'application/pdf' || file.filename.toLowerCase().endsWith('.pdf');
}
