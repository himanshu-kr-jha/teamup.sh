import { describe, expect, it } from 'vitest';
import type { CallNotesState } from '@/shared/api/client';
import { NOTES_TOPIC, notesAction, notesBannerText, notesFromPacket, pauseText, transcriptWithGaps } from './notetaker';

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

describe('the transcript page', () => {
  const said = (at: string, text: string) => ({ speaker: 'Ada', startedAt: `2026-11-18T${at}:00Z`, text });
  it('is in the order things were said, with a line where notes were paused', () => {
    // Kept in the order speech to text answered, not the order they were said.
    const lines = [said('10:09', 'clip 10'), said('10:01', 'clip 8'), said('10:03', 'clip 9'), said('10:15', 'clip 11')];
    const got = transcriptWithGaps(lines, [{ from: '2026-11-18T10:05:00Z', to: '2026-11-18T10:12:00Z', by: 'Ravi' }], time);
    expect(got.map((e) => e.text)).toEqual(['clip 8', 'clip 9', 'Notes were paused from 10:05 to 10:12 by Ravi.', 'clip 10', 'clip 11']);
    expect(got[2].kind).toBe('gap');
  });

  it('says where notes began when they were off from the start', () => {
    const got = transcriptWithGaps([said('10:20', 'hello')], [{ to: '2026-11-18T10:12:00Z', resumedBy: 'Mia' }], time);
    expect(got.map((e) => e.text)).toEqual(['Notes were off until 10:12, when Mia started them.', 'hello']);
  });
});
