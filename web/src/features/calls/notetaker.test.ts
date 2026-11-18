import { describe, expect, it } from 'vitest';
import type { CallNotesState } from '@/shared/api/client';
import { NOTES_TOPIC, notesAction, notesBannerText, notesFromPacket, pauseText } from './notetaker';

const time = (at: string) => at.slice(11, 16);
const state = (on: boolean, changes: CallNotesState['changes'] = []): CallNotesState => ({ callId: 'c1', available: true, on, changes });
const paused = state(false, [{ on: false, name: 'Ravi Rao', at: '2026-11-18T10:05:00Z' }]);

describe('the notes banner', () => {
  it('says notes are taken only while the note-taker is there', () => {
    expect(notesBannerText(state(true), false, false, time)).toBeNull();
    expect(notesBannerText(state(true), true, false, time)).toEqual({
      tone: 'on',
      text: 'Notes are being taken. Everybody here gets the minutes when the call ends.',
    });
    expect(notesBannerText(undefined, true, true, time)?.text).toContain('including what you say');
  });

  it('says who paused them and when, to members and guests alike', () => {
    for (const guest of [false, true]) {
      expect(notesBannerText(paused, true, guest, time)).toEqual({
        tone: 'paused',
        text: 'Notes paused by Ravi Rao at 10:05. Nothing said now is written down.',
      });
    }
  });

  it('says nothing for notes off from the start', () => {
    expect(notesBannerText(state(false), false, false, time)).toBeNull();
  });
});

describe('the notes button', () => {
  it('pauses, resumes, or starts notes that were off from the start', () => {
    expect(notesAction(state(true))).toBe('Pause notes');
    expect(notesAction(paused)).toBe('Resume notes');
    expect(notesAction(state(false))).toBe('Start notes');
  });
});

describe('a gap in the notes', () => {
  it('is said plainly', () => {
    expect(pauseText({ from: '2026-11-18T10:05:00Z', to: '2026-11-18T10:12:00Z', by: 'Ravi' }, time)).toBe(
      'Notes were paused from 10:05 to 10:12 by Ravi.'
    );
    expect(pauseText({ from: '2026-11-18T10:05:00Z', by: 'Ravi' }, time)).toBe(
      'Notes were paused at 10:05 by Ravi, until the end of the call.'
    );
    expect(pauseText({ to: '2026-11-18T10:12:00Z', resumedBy: 'Mia' }, time)).toBe('Notes were off until 10:12, when Mia started them.');
  });
});

describe('the room telling everybody', () => {
  const packet = (body: unknown) => new TextEncoder().encode(JSON.stringify(body));
  it('is believed only from the server, on its own topic', () => {
    expect(notesFromPacket(packet(paused), undefined, NOTES_TOPIC)).toEqual(paused);
    expect(notesFromPacket(packet(paused), { identity: 'someone' }, NOTES_TOPIC)).toBeNull();
    expect(notesFromPacket(packet(paused), undefined, 'teamup.meeting-chat')).toBeNull();
    expect(notesFromPacket(new TextEncoder().encode('not json'), undefined, NOTES_TOPIC)).toBeNull();
    expect(notesFromPacket(packet({ on: 'yes' }), undefined, NOTES_TOPIC)).toBeNull();
  });
});
