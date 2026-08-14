import { useState } from 'react';
import { filesApi, type MultipartUpload, type PartUpload } from '@/shared/api/client';
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

// The server's numbers, asked for once per page load. Fetching them is a
// courtesy so an over-sized file fails here instead of after the bytes have
// crossed the network, and so the browser picks the same upload path the
// server would; the enforcement is the content length signed into every URL,
// which object storage itself refuses a different body for.
interface UploadLimits {
  maxUploadBytes: number;
  multipartThresholdBytes: number;
}

// Matches the server's own defaults (internal/files). Used only when the
// limits call fails: guessing "no limit" would send a 2 GB file down the
// single-PUT path the server refuses.
const FALLBACK_LIMITS: UploadLimits = {
  maxUploadBytes: Number.POSITIVE_INFINITY,
  multipartThresholdBytes: 16 * 1024 * 1024,
};

let limitsPromise: Promise<UploadLimits> | null = null;

function uploadLimits(): Promise<UploadLimits> {
  if (!limitsPromise) {
    limitsPromise = filesApi
      .limits()
      .then(({ data }) => ({
        maxUploadBytes: data.maxUploadBytes,
        multipartThresholdBytes: data.multipartThresholdBytes || FALLBACK_LIMITS.multipartThresholdBytes,
      }))
      .catch(() => {
        // Do not block an upload because this one call failed; the server
        // still refuses anything too large. Try again next time.
        limitsPromise = null;
        return FALLBACK_LIMITS;
      });
  }
  return limitsPromise;
}

// ── Remembering an upload across a reload ────────────────────────────────
//
// A reloaded tab cannot re-open a File it was given: the handle does not
// survive, and the user has to pick the file again. What CAN survive is a note
// of which server-side upload that file belongs to, so when they pick it again
// the parts already in storage are kept.
//
// Resuming is deliberately gated on this note matching name, size AND
// lastModified. Matching on name and size alone would eventually splice two
// different recordings into one object — the parts already in storage are not
// re-read or re-checksummed, so a wrong match is silent corruption, not an
// error. Better to re-upload than to assemble a file that never existed.

const FINGERPRINT_PREFIX = 'teamup.upload.';

function fingerprint(category: string, refId: string, file: File): string {
  return `${FINGERPRINT_PREFIX}${category}:${refId}:${file.name}:${file.size}:${file.lastModified}`;
}

function rememberUpload(category: string, refId: string, file: File, uploadId: string) {
  try {
    localStorage.setItem(fingerprint(category, refId, file), uploadId);
  } catch {
    // Private browsing, or a full quota. Resuming across a reload is a bonus,
    // not a precondition; the upload itself is unaffected.
  }
}

function recallUpload(category: string, refId: string, file: File): string | null {
  try {
    return localStorage.getItem(fingerprint(category, refId, file));
  } catch {
    return null;
  }
}

function forgetUpload(category: string, refId: string, file: File) {
  try {
    localStorage.removeItem(fingerprint(category, refId, file));
  } catch {
    /* nothing to clean up */
  }
}

// ── Tuning ───────────────────────────────────────────────────────────────

/** Parts in flight at once. Enough to fill a fast link, few enough not to
 *  starve the rest of the app of connections. */
const PART_CONCURRENCY = 3;

/** How many times the whole "ask what is missing, send it" round may repeat
 *  before giving up. Each round gets fresh URLs, so an expired signature costs
 *  a round rather than the upload. */
const MAX_ROUNDS = 6;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Uploading is three steps: ask for somewhere to put the bytes, send them, and
 * tell the server it happened.
 *
 * Small files take one PUT. Anything over the server's threshold goes up in
 * parts, because one unresumable request for a 2 GB screen recording is how an
 * upload comes to fail at 80% and start again from nothing. The part path
 * resumes: after a failure it asks the server what is still missing and sends
 * only that, and after a page reload it picks the same upload back up when the
 * user re-selects the same file.
 */
export function useUpload(category: string, refId: string) {
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState('');

  const upload = async (file: File): Promise<UploadedFile | null> => {
    setError('');
    setProgress(0);
    try {
      const limits = await uploadLimits();
      if (file.size > limits.maxUploadBytes) {
        setError(
          `${file.name} is ${humanSize(file.size)}. The largest file this server accepts is ${humanSize(limits.maxUploadBytes)}.`
        );
        return null;
      }

      const contentType = file.type || 'application/octet-stream';
      if (file.size > limits.multipartThresholdBytes) {
        return await uploadInParts(category, refId, file, contentType, setProgress);
      }
      return await uploadInOnePut(category, refId, file, contentType, setProgress);
    } catch (err) {
      setError(apiErrorMessage(err, 'Could not upload that file.'));
      return null;
    } finally {
      setProgress(null);
    }
  };

  return { upload, progress, error, clearError: () => setError('') };
}

// ── One PUT ──────────────────────────────────────────────────────────────

async function uploadInOnePut(
  category: string,
  refId: string,
  file: File,
  contentType: string,
  onProgress: (pct: number) => void
): Promise<UploadedFile> {
  const { data: presigned } = await filesApi.getUploadUrl({
    category,
    refId,
    filename: file.name,
    contentType,
    size: file.size,
  });

  await putBlob(presigned.uploadUrl, file, presigned.headers, (loaded) =>
    onProgress(percent(loaded, file.size))
  );

  const { data: recorded } = await filesApi.record({
    category,
    refId,
    objectKey: presigned.objectKey,
    filename: file.name,
    contentType,
    size: file.size,
  });

  return {
    id: recorded.id,
    objectKey: presigned.objectKey,
    filename: file.name,
    contentType,
    // What storage says is there, not what we claimed on the way in.
    size: recorded.sizeBytes,
  };
}

// ── Many parts ───────────────────────────────────────────────────────────

async function uploadInParts(
  category: string,
  refId: string,
  file: File,
  contentType: string,
  onProgress: (pct: number) => void
): Promise<UploadedFile> {
  const upload = await findOrStartUpload(category, refId, file, contentType);
  rememberUpload(category, refId, file, upload.id);

  // Whatever already landed — from an earlier attempt, or an earlier page.
  const { data: status } = await filesApi.multipartStatus(upload.id);
  const done = new Set(status.uploaded.map((p) => p.partNumber));
  let settledBytes = status.uploaded.reduce((n, p) => n + p.size, 0);
  const inFlight = new Map<number, number>();

  const report = () => {
    let loaded = settledBytes;
    for (const n of inFlight.values()) loaded += n;
    onProgress(percent(loaded, upload.totalBytes));
  };
  report();

  let lastError: unknown = null;
  for (let round = 0; done.size < upload.partCount; round++) {
    if (round >= MAX_ROUNDS) {
      throw lastError ?? new Error('Upload stalled. It will resume from here if you try again.');
    }

    // Ask what is missing rather than tracking it: object storage is the only
    // thing that cannot disagree with itself about which parts it holds. Fresh
    // URLs each round, so an expired signature is not fatal.
    const { data } = await filesApi.signParts(upload.id);
    if (data.parts.length === 0) break;

    const failures = await sendParts(file, upload, data.parts, {
      onPartProgress: (partNumber, loaded) => {
        inFlight.set(partNumber, loaded);
        report();
      },
      onPartDone: (part) => {
        inFlight.delete(part.partNumber);
        done.add(part.partNumber);
        settledBytes += part.contentLength;
        report();
      },
      onPartFailed: (part) => {
        inFlight.delete(part.partNumber);
        report();
      },
    });

    if (failures.length > 0) {
      lastError = failures[0];
      // Back off before asking again: a flapping connection recovers, and a
      // tight loop would burn the round budget in a second.
      await sleep(Math.min(1000 * 2 ** round, 8000));
    } else {
      round = -1; // progress was made; the budget is for consecutive failures
    }
  }

  const { data: recorded } = await filesApi.completeMultipart(upload.id);
  forgetUpload(category, refId, file);

  return {
    id: recorded.id,
    objectKey: recorded.objectKey,
    filename: recorded.filename,
    contentType: recorded.contentType || contentType,
    // Measured by storage after assembly, not claimed by this browser.
    size: recorded.sizeBytes,
  };
}

/**
 * Resumes the upload this exact file was last part of, or starts a new one.
 *
 * The remembered id is checked against the server's list rather than trusted:
 * it may have been swept, aborted from another tab, or already finished.
 */
async function findOrStartUpload(
  category: string,
  refId: string,
  file: File,
  contentType: string
): Promise<MultipartUpload> {
  const remembered = recallUpload(category, refId, file);
  if (remembered) {
    try {
      const { data } = await filesApi.listMultipartUploads(category, refId);
      const match = data.find((u) => u.id === remembered && u.totalBytes === file.size);
      if (match) return match;
    } catch {
      // Fall through and start a fresh upload rather than failing the whole
      // thing because resuming was not possible.
    }
    forgetUpload(category, refId, file);
  }

  const { data } = await filesApi.initiateMultipart({
    category,
    refId,
    filename: file.name,
    contentType,
    size: file.size,
  });
  return data;
}

interface PartHandlers {
  onPartProgress: (partNumber: number, loaded: number) => void;
  onPartDone: (part: PartUpload) => void;
  onPartFailed: (part: PartUpload) => void;
}

/** Sends a batch of parts, a few at a time, and reports which ones failed. */
async function sendParts(
  file: File,
  upload: MultipartUpload,
  parts: PartUpload[],
  handlers: PartHandlers
): Promise<unknown[]> {
  const queue = [...parts];
  const failures: unknown[] = [];

  const worker = async () => {
    for (;;) {
      const part = queue.shift();
      if (!part) return;
      const start = (part.partNumber - 1) * upload.partSize;
      // Exactly the number of bytes the URL was signed for. Anything else is
      // refused by object storage before it is stored.
      const slice = file.slice(start, start + part.contentLength);
      try {
        await putBlob(part.url, slice, undefined, (loaded) =>
          handlers.onPartProgress(part.partNumber, loaded)
        );
        handlers.onPartDone(part);
      } catch (err) {
        failures.push(err);
        handlers.onPartFailed(part);
      }
    }
  };

  await Promise.all(Array.from({ length: Math.min(PART_CONCURRENCY, queue.length) }, worker));
  return failures;
}

function percent(loaded: number, total: number): number {
  if (total <= 0) return 0;
  return Math.min(100, Math.round((loaded / total) * 100));
}

// XHR rather than fetch: it is the only way to observe upload progress.
function putBlob(
  url: string,
  body: Blob,
  headers: Record<string, string> | undefined,
  onProgress: (loaded: number) => void
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', url);
    for (const [k, v] of Object.entries(headers ?? {})) xhr.setRequestHeader(k, v);

    xhr.upload.onprogress = (e) => onProgress(e.loaded);
    xhr.onload = () =>
      xhr.status >= 200 && xhr.status < 300
        ? resolve()
        : reject(
            new Error(
              // 403 here is the signature being rejected: either the body is
              // not the length the URL was issued for, or the URL has expired.
              // Both are recoverable by asking for a fresh one.
              xhr.status === 403
                ? 'Upload rejected: the file changed while it was being sent, or the link expired.'
                : `Upload failed (${xhr.status})`
            )
          );
    xhr.onerror = () => reject(new Error('Upload failed. Check your connection.'));
    xhr.send(body);
  });
}
