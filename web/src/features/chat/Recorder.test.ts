import { describe, expect, it } from 'vitest';
import { clock, recordingFormat, recordingName } from './Recorder';

describe('recording', () => {
  it('picks WebM where the browser has it, and MP4 on Safari', () => {
    expect(recordingFormat('audio', (t) => t.startsWith('audio/webm'))).toEqual({ mime: 'audio/webm;codecs=opus', ext: 'webm' });
    expect(recordingFormat('audio', (t) => t === 'audio/mp4')).toEqual({ mime: 'audio/mp4', ext: 'mp4' });
    expect(recordingFormat('video', (t) => t === 'video/mp4')).toEqual({ mime: 'video/mp4', ext: 'mp4' });
    expect(recordingFormat('video', () => false)).toBeNull();
  });

  it('names a recording by what it is and when', () => {
    expect(recordingName('audio', 'webm', new Date(2026, 10, 8, 9, 5, 3))).toBe('Voice message 2026-11-08 09.05.03.webm');
    expect(recordingName('video', 'mp4', new Date(2026, 0, 1, 23, 0, 0))).toBe('Video message 2026-01-01 23.00.00.mp4');
  });

  it('shows minutes and seconds', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(65.7)).toBe('1:05');
  });
});
