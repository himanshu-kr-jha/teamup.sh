import { describe, expect, it } from 'vitest';
import {
  MOVED_TOPIC,
  countPeople,
  deviceId,
  deviceKind,
  deviceLabel,
  movedMessage,
  movedPacket,
  newDeviceId,
  personOf,
  readMoved,
  samePerson,
} from './devices';
import { handQueue } from './handQueue';
import { parseCallMemory } from './callMemory';

const ASHA = '6f1c2b8e-3d4a-4f5b-9c6d-7e8f9a0b1c2d';
const BILAL = '0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
const laptop = `${ASHA}:lap1`;
const phone = `${ASHA}:ph1`;

function memoryStorage() {
  const data = new Map<string, string>();
  return { data, getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) };
}

describe('who an identity is', () => {
  it('is the same person from every device, an older bare id included', () => {
    expect(personOf(laptop)).toBe(ASHA);
    expect(personOf(phone)).toBe(ASHA);
    expect(personOf(ASHA)).toBe(ASHA);
    expect(samePerson(laptop, phone)).toBe(true);
    expect(samePerson(laptop, `${BILAL}:lap1`)).toBe(false);
  });

  it('keeps guests as themselves and the note-taker as nobody', () => {
    expect(personOf('guest-11111111-2222-4333-8444-555555555555')).toBe('guest-11111111-2222-4333-8444-555555555555');
    expect(personOf('notes-abc')).toBe('');
    expect(samePerson('notes-a', 'notes-a')).toBe(false);
  });

  it('counts each person once', () => {
    expect(countPeople([laptop, phone, `${BILAL}:x`, 'guest-g1', 'notes-c'])).toBe(3);
    expect(countPeople([laptop, phone])).toBe(1);
    expect(countPeople([])).toBe(0);
  });
});

describe('this device', () => {
  it('makes an id once and keeps it, so a reload is the same device', () => {
    const storage = memoryStorage();
    const first = deviceId(storage);
    expect(first).toMatch(/^[A-Za-z0-9]{16}$/);
    expect(deviceId(storage)).toBe(first);
  });

  it('does not trust an id that is not what it writes', () => {
    const storage = memoryStorage();
    storage.setItem('teamup.device.id', 'a:b');
    expect(deviceId(storage)).toMatch(/^[A-Za-z0-9]{16}$/);
  });

  it('makes ids from the whole alphabet', () => {
    expect(newDeviceId((n) => new Uint8Array(n).fill(0))).toBe('A'.repeat(16));
    expect(newDeviceId((n) => new Uint8Array(n).fill(61))).toBe('9'.repeat(16));
  });

  it('tells a phone, a tablet and a laptop apart', () => {
    const android = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36';
    const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1';
    const androidTablet = 'Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36';
    const ipad = 'Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1';
    const mac = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';
    const windows = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36';
    expect(deviceKind(android)).toBe('phone');
    expect(deviceKind(iphone)).toBe('phone');
    expect(deviceKind(androidTablet)).toBe('tablet');
    expect(deviceKind(ipad)).toBe('tablet');
    // An iPad asking for the desktop site says it is a Mac, with a touch screen.
    expect(deviceKind(mac, 5)).toBe('tablet');
    expect(deviceKind(mac, 0)).toBe('laptop');
    expect(deviceKind(windows)).toBe('laptop');
  });
});

describe('tiles', () => {
  it('name the device only while the person is in from more than one', () => {
    expect(deviceLabel('Asha Archer', 'phone', 2)).toBe('Asha Archer (phone)');
    expect(deviceLabel('Asha Archer', 'phone', 1)).toBe('Asha Archer');
    expect(deviceLabel('Asha Archer', undefined, 2)).toBe('Asha Archer');
  });
});

describe('moving the call to another device', () => {
  const packet = movedPacket('phone');
  it('is heard only from another device of the same person', () => {
    expect(readMoved(packet, { identity: phone }, MOVED_TOPIC, laptop)).toEqual({ to: 'phone' });
    expect(readMoved(packet, { identity: `${BILAL}:x` }, MOVED_TOPIC, laptop)).toBeNull();
    expect(readMoved(packet, { identity: laptop }, MOVED_TOPIC, laptop)).toBeNull();
    expect(readMoved(packet, undefined, MOVED_TOPIC, laptop)).toBeNull();
    expect(readMoved(packet, { identity: phone }, 'teamup.meeting-chat', laptop)).toBeNull();
    expect(readMoved(new TextEncoder().encode('{"to":"toaster"}'), { identity: phone }, MOVED_TOPIC, laptop)).toEqual({ to: undefined });
  });

  it('says where it went', () => {
    expect(movedMessage('phone')).toBe('Moved to your phone. This device has left the call.');
    expect(movedMessage(undefined)).toBe('Moved to your other device. This device has left the call.');
  });
});

describe('raised hands', () => {
  it('are one per person, on the tile that has had it longest', () => {
    const at = '2026-11-19T10:00:00.000Z';
    const queue = handQueue([
      { identity: phone, name: 'Asha Archer', attributes: { hand: at } },
      { identity: laptop, name: 'Asha Archer', attributes: { hand: at } },
      { identity: `${BILAL}:b`, name: 'Bilal Baker', attributes: { hand: '2026-11-19T10:00:05.000Z' } },
    ]);
    expect(queue.map((h) => [h.identity, h.device, h.position])).toEqual([
      [ASHA, laptop, 1],
      [BILAL, `${BILAL}:b`, 2],
    ]);
    expect(queue[0].raisedAt).toBe(at);
  });
});

describe('a reload of a second camera', () => {
  it('comes back as one', () => {
    const raw = JSON.stringify({ userId: 'u1', callId: 'c1', at: 1, mic: false, camera: true, companion: true });
    expect(parseCallMemory(raw)?.companion).toBe(true);
    expect(parseCallMemory(JSON.stringify({ userId: 'u1', callId: 'c1', at: 1 }))?.companion).toBeUndefined();
  });
});
