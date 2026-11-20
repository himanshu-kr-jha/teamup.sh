// What the Appearance tab does with a picture before it is sent as one of the
// organization's call backgrounds. The server checks and prepares every one
// again (internal/org/call_pictures.go); this is so a wrong one is said at once,
// and a photograph straight off a phone, too large to send, still goes in.

/** The most the server takes, which is also the most any request may carry. */
export const MAX_PICTURE_BYTES = 2 * 1024 * 1024;
/** How many pictures an organization may have. */
export const MAX_PICTURES = 24;
export const PICTURE_TYPES = ['image/png', 'image/jpeg', 'image/webp'];
/** The camera's own width: anything narrower would be stretched behind everybody. */
const MIN_WIDTH = 1280;
/** What a picture is brought to when it is too large to send as it is. */
const SEND_WIDTH = 1920;
const MAX_PIXELS = 40_000_000;

/** A name for a picture from its file's: "harbour-at-dusk.jpg" is "Harbour at dusk". */
export function nameFromFile(fileName: string): string {
  const base = fileName.replace(/\.[a-z0-9]{2,5}$/i, '');
  const words = base.replace(/[_\-.]+/g, ' ').replace(/\s+/g, ' ').trim();
  if (!words) return 'Background';
  return (words[0].toUpperCase() + words.slice(1)).slice(0, 60).trim();
}

/**
 * The largest 16:9 part of a picture of the given size, from its middle, as the
 * server cuts it.
 */
export function coverCrop(width: number, height: number) {
  let w = width;
  let h = Math.floor((width * 9) / 16);
  if (h > height) {
    w = Math.floor((height * 16) / 9);
    h = height;
  }
  return { x: Math.floor((width - w) / 2), y: Math.floor((height - h) / 2), width: w, height: h };
}

/** Why a picture of this size will not do, or empty when it will. */
export function sizeProblem(width: number, height: number): string {
  if (coverCrop(width, height).width < MIN_WIDTH) return 'The picture must be at least 1280 x 720.';
  return '';
}

/**
 * The picture to send for a file: the file itself when the server can take it as
 * it is, else a 16:9 copy at 1920 x 1080 made here. Throws with the reason when
 * it will not do.
 */
export async function pictureToSend(file: File): Promise<Blob> {
  if (!PICTURE_TYPES.includes(file.type)) throw new Error('The picture must be a PNG, JPEG or WebP file.');
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    try {
      await img.decode();
    } catch {
      throw new Error('That picture could not be read.');
    }
    const { naturalWidth: w, naturalHeight: h } = img;
    const problem = sizeProblem(w, h);
    if (problem) throw new Error(problem);
    if (file.size <= MAX_PICTURE_BYTES && w * h <= MAX_PIXELS) return file;
    const crop = coverCrop(w, h);
    const canvas = document.createElement('canvas');
    canvas.width = SEND_WIDTH;
    canvas.height = (SEND_WIDTH * 9) / 16;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('This browser cannot prepare that picture.');
    ctx.imageSmoothingQuality = 'high';
    ctx.drawImage(img, crop.x, crop.y, crop.width, crop.height, 0, 0, canvas.width, canvas.height);
    for (const quality of [0.9, 0.8, 0.7]) {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
      if (blob && blob.size <= MAX_PICTURE_BYTES) return blob;
    }
    throw new Error('That picture is too large: the most is 2 MB.');
  } finally {
    URL.revokeObjectURL(url);
  }
}
