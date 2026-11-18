import { describe, expect, it } from 'vitest';
import { deviceProblem, inCallText, inputDevice, markReported, mediaFailure, wasReported } from './mediaErrors';

const err = (name: string) => new DOMException('x', name);

describe('deviceProblem', () => {
  it('reads a refused permission, a missing device, a busy one, and anything else', () => {
    expect(deviceProblem(err('NotAllowedError'))).toBe('denied');
    expect(deviceProblem(err('SecurityError'))).toBe('denied');
    expect(deviceProblem({ name: 'PermissionDeniedError' })).toBe('denied');
    expect(deviceProblem(err('NotFoundError'))).toBe('missing');
    expect(deviceProblem({ name: 'DevicesNotFoundError' })).toBe('missing');
    // A remembered device id that is no longer there.
    expect(deviceProblem(err('OverconstrainedError'))).toBe('missing');
    expect(deviceProblem(err('NotReadableError'))).toBe('busy');
    expect(deviceProblem({ name: 'TrackStartError' })).toBe('busy');
    expect(deviceProblem(err('AbortError'))).toBe('busy');
    expect(deviceProblem(new Error('boom'))).toBe('failed');
    expect(deviceProblem(null)).toBe('failed');
    expect(deviceProblem(undefined)).toBe('failed');
  });
});

describe('inputDevice', () => {
  it('is the camera or the microphone, and nothing for a screen or a speaker', () => {
    expect(inputDevice('videoinput')).toBe('camera');
    expect(inputDevice('audioinput')).toBe('microphone');
    expect(inputDevice('audiooutput')).toBeNull();
    expect(inputDevice(undefined)).toBeNull();
  });
});

describe('inCallText', () => {
  it('names the device and says what to do', () => {
    expect(inCallText('denied', 'camera')).toBe('Your browser blocked the camera. Allow it in the address bar, then turn the camera on again.');
    expect(inCallText('denied', 'microphone')).toBe(
      'Your browser blocked the microphone. Allow it in the address bar, then turn the microphone on again.'
    );
    expect(inCallText('missing', 'camera')).toBe('No camera was found. Plug one in or choose another, then turn the camera on again.');
    expect(inCallText('busy', 'microphone')).toBe(
      'Your microphone is in use by another app, or could not start. Close that app, then turn the microphone on again.'
    );
    expect(inCallText('failed', 'camera')).toBe('Your camera could not be started. Turn it on again to try once more.');
  });

  it('never blames the permission for a device that is busy or missing', () => {
    for (const problem of ['missing', 'busy', 'failed'] as const) {
      expect(inCallText(problem, 'camera')).not.toMatch(/permission|blocked|allow/i);
    }
  });
});

describe('mediaFailure', () => {
  it('is said for the camera and the microphone', () => {
    expect(mediaFailure(err('NotReadableError'), 'videoinput')).toEqual({
      device: 'camera',
      problem: 'busy',
      text: inCallText('busy', 'camera'),
    });
    expect(mediaFailure(err('NotAllowedError'), 'audioinput')?.device).toBe('microphone');
  });

  it('is nothing for a closed screen picker or a speaker', () => {
    expect(mediaFailure(err('NotAllowedError'), undefined)).toBeNull();
    expect(mediaFailure(err('NotFoundError'), 'audiooutput')).toBeNull();
  });
});

describe('markReported', () => {
  it('remembers an error the banner has shown, so a toast does not repeat it', () => {
    const shown = err('NotReadableError');
    const other = err('NotReadableError');
    markReported(shown);
    expect(wasReported(shown)).toBe(true);
    expect(wasReported(other)).toBe(false);
    markReported(null);
    expect(wasReported(null)).toBe(false);
  });
});
