/**
 * A picture cut to the square in its middle and drawn at `size` pixels, as a
 * JPEG data URL.
 *
 * Drawing it again is what drops whatever the camera wrote into the file,
 * location included, and it makes a phone's 4 MB photo a few tens of KB.
 */
export async function squareImage(file: Blob, size = 256): Promise<string> {
  const bitmap = await createImageBitmap(file);
  try {
    const { sx, sy, side } = centreSquare(bitmap.width, bitmap.height);
    const canvas = document.createElement('canvas');
    canvas.width = size;
    canvas.height = size;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('This browser cannot resize pictures.');
    // JPEG has no transparency; a transparent picture goes on white, not black.
    context.fillStyle = '#ffffff';
    context.fillRect(0, 0, size, size);
    context.imageSmoothingQuality = 'high';
    context.drawImage(bitmap, sx, sy, side, side, 0, 0, size, size);
    return canvas.toDataURL('image/jpeg', 0.88);
  } finally {
    bitmap.close();
  }
}

/** The geometry of that cut, kept apart so it can be tested without a canvas. */
export function centreSquare(width: number, height: number): { sx: number; sy: number; side: number } {
  const side = Math.min(width, height);
  return { sx: (width - side) / 2, sy: (height - side) / 2, side };
}
