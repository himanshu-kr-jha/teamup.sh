import { deflateSync } from 'node:zlib';
import type { Api } from './api';

// The organization's background pictures, for the specs that put one behind a
// camera: since 19 November 2026 they are the only pictures there are, so a
// spec adds what it needs through the API, as an administrator would on
// Organization > Appearance, and takes it away again afterwards.

export type Rgb = [number, number, number];

/** Colours easy to tell apart on a camera: a dark blue-grey, a warm light one, a red. */
export const COLOURS = {
  night: [20, 26, 48] as Rgb,
  dawn: [250, 200, 150] as Rgb,
  red: [220, 30, 30] as Rgb,
};

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(buf: Buffer): number {
  let c = 0xffffffff;
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([length, body, crc]);
}

/** A picture of one colour, as a PNG, at the camera's size unless told otherwise. */
export function solidPng(rgb: Rgb, width = 1280, height = 720): Buffer {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bits per channel
  header[9] = 2; // RGB
  const row = Buffer.alloc(1 + width * 3);
  for (let x = 0; x < width; x++) row.set(rgb, 1 + x * 3);
  const raw = Buffer.concat(Array.from({ length: height }, () => row));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

export interface OrgPicture {
  id: string;
  name: string;
  url: string;
  thumbUrl: string;
}

/** Adds pictures of the given colours, by name, and says what they are. */
export async function addOrgPictures(admin: Api, pictures: Record<string, Rgb>): Promise<OrgPicture[]> {
  const added: OrgPicture[] = [];
  for (const [name, rgb] of Object.entries(pictures)) {
    const res = await admin.ctx.post(`/api/org/call-pictures?name=${encodeURIComponent(name)}`, {
      data: solidPng(rgb),
      headers: { 'Content-Type': 'image/png' },
    });
    if (res.status() !== 201) throw new Error(`adding ${name}: ${res.status()} ${await res.text()}`);
    const view = (await res.json()) as { pictures: OrgPicture[] };
    added.push(view.pictures[view.pictures.length - 1]);
  }
  return added;
}

/** Takes every picture away, so a spec starts from none. */
export async function removeAllOrgPictures(admin: Api) {
  const view = await admin.get<{ pictures?: OrgPicture[] }>('/org/call-backgrounds');
  for (const p of view.pictures ?? []) await admin.del(`/org/call-pictures/${p.id}`);
}

export async function removeOrgPictures(admin: Api, pictures: OrgPicture[]) {
  for (const p of pictures) await admin.del(`/org/call-pictures/${p.id}`).catch(() => undefined);
}
