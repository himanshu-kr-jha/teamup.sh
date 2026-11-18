import { describe, expect, it } from 'vitest';
import {
  REJOIN_WITHIN_MS,
  forgetCall,
  parseCallMemory,
  recallCall,
  rememberCall,
  type CallMemory,
} from './callMemory';

function memoryStorage() {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
  };
}

const NOW = 1_800_000_000_000;
const call: CallMemory = {
  userId: 'u1',
  callId: 'c1',
  standing: false,
  mic: true,
  camera: false,
  audioInput: 'mic-2',
  background: { kind: 'blur', strength: 'light' },
  at: NOW,
};

describe('the call kept across a reload', () => {
  it('comes back after a reload, as it was', () => {
    const storage = memoryStorage();
    rememberCall(call, storage);
    expect(recallCall('u1', NOW + 3_000, { reloaded: true, storage })).toEqual({
      ...call,
      videoInput: undefined,
      audioOutput: undefined,
    });
  });

  it('does not come back on a page that was not reloaded, such as a duplicated tab', () => {
    const storage = memoryStorage();
    rememberCall(call, storage);
    expect(recallCall('u1', NOW + 3_000, { reloaded: false, storage })).toBeNull();
  });

  it('does not come back for somebody else signed in on the tab', () => {
    const storage = memoryStorage();
    rememberCall(call, storage);
    expect(recallCall('u2', NOW + 3_000, { reloaded: true, storage })).toBeNull();
    expect(recallCall(undefined, NOW + 3_000, { reloaded: true, storage })).toBeNull();
  });

  it('does not come back once too long has passed, or from the future', () => {
    const storage = memoryStorage();
    rememberCall(call, storage);
    expect(recallCall('u1', NOW + REJOIN_WITHIN_MS - 1, { reloaded: true, storage })).not.toBeNull();
    expect(recallCall('u1', NOW + REJOIN_WITHIN_MS + 1, { reloaded: true, storage })).toBeNull();
    expect(recallCall('u1', NOW - 60_000, { reloaded: true, storage })).toBeNull();
  });

  it('is gone once forgotten', () => {
    const storage = memoryStorage();
    rememberCall(call, storage);
    forgetCall(storage);
    expect(recallCall('u1', NOW, { reloaded: true, storage })).toBeNull();
  });

  it('reads anything that is not what was written as nothing', () => {
    expect(parseCallMemory(null)).toBeNull();
    expect(parseCallMemory('not json')).toBeNull();
    expect(parseCallMemory('[]')).toBeNull();
    expect(parseCallMemory(JSON.stringify({ callId: 'c1', at: NOW }))).toBeNull();
    expect(parseCallMemory(JSON.stringify({ userId: 'u1', callId: 'c1', at: 'soon' }))).toBeNull();
    // Only what is true is on, and a background of a shape never written is dropped.
    expect(
      parseCallMemory(
        JSON.stringify({ userId: 'u1', callId: 'c1', at: NOW, mic: 'yes', background: { kind: 'blur', strength: 'max' } })
      )
    ).toEqual({
      userId: 'u1',
      callId: 'c1',
      at: NOW,
      standing: false,
      mic: false,
      camera: false,
      audioInput: undefined,
      videoInput: undefined,
      audioOutput: undefined,
      background: undefined,
    });
  });

  it('does without storage that refuses', () => {
    const refuse = () => {
      throw new Error('denied');
    };
    const refusing = { getItem: refuse, setItem: refuse, removeItem: refuse };
    expect(() => rememberCall(call, refusing)).not.toThrow();
    expect(() => forgetCall(refusing)).not.toThrow();
    expect(recallCall('u1', NOW, { reloaded: true, storage: refusing })).toBeNull();
  });
});
