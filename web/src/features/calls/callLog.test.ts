import { describe, expect, it } from 'vitest';
import { describeEvent, orderEvents, peopleFromSessions, formatMinutes, type CallEvent, type CallSession } from './callLog';

const event = (type: string, extra: Partial<CallEvent> = {}): CallEvent => ({
  id: type,
  type: type as CallEvent['type'],
  userId: 'u1',
  userName: 'Ann',
  actorId: 'u2',
  actorName: 'Mia',
  at: '2026-11-07T09:00:00Z',
  ...extra,
});

describe('describeEvent', () => {
  it('says each kind in plain words', () => {
    expect(describeEvent(event('knocked'))).toBe('Ann asked to join');
    expect(describeEvent(event('admitted'))).toBe('Mia let Ann in');
    expect(describeEvent(event('denied'))).toBe('Mia turned Ann away');
    expect(describeEvent(event('joined'))).toBe('Ann joined');
    expect(describeEvent(event('left'))).toBe('Ann left');
    expect(describeEvent(event('screen_share_started'))).toBe('Ann started sharing their screen');
    expect(describeEvent(event('screen_share_stopped'))).toBe('Ann stopped sharing their screen');
  });

  it('copes with a name that is missing', () => {
    expect(describeEvent(event('joined', { userName: '' }))).toBe('Somebody joined');
    expect(describeEvent(event('admitted', { actorName: undefined }))).toBe('Somebody let Ann in');
  });

  it('does not fall over on a kind it has not heard of', () => {
    expect(describeEvent(event('renamed'))).toBe('Ann: renamed');
  });
});

describe('peopleFromSessions', () => {
  const session = (userId: string, userName: string, joinedAt: string | null, leftAt: string | null): CallSession => ({
    userId,
    userName,
    joinedAt,
    leftAt,
  });
  const NOW = new Date('2026-11-07T10:00:00Z');

  it('rolls every session of a person into one line', () => {
    const [ann, bob] = peopleFromSessions(
      [
        session('a', 'Ann', '2026-11-07T09:00:00Z', '2026-11-07T09:10:00Z'),
        session('b', 'Bob', '2026-11-07T09:05:00Z', '2026-11-07T09:06:00Z'),
        session('a', 'Ann', '2026-11-07T09:20:00Z', '2026-11-07T09:30:00Z'),
      ],
      NOW
    );
    expect(ann).toMatchObject({ name: 'Ann', sessions: 2, minutes: 20, stillHere: false });
    expect(ann.firstJoinAt).toBe('2026-11-07T09:00:00Z');
    expect(ann.lastLeaveAt).toBe('2026-11-07T09:30:00Z');
    expect(bob.minutes).toBe(1);
  });

  it('does not double count two devices overlapping', () => {
    const [ann] = peopleFromSessions(
      [
        session('a', 'Ann', '2026-11-07T09:00:00Z', '2026-11-07T09:10:00Z'),
        session('a', 'Ann', '2026-11-07T09:05:00Z', '2026-11-07T09:15:00Z'),
      ],
      NOW
    );
    expect(ann.minutes).toBe(15);
  });

  it('counts somebody still connected up to now', () => {
    const [ann] = peopleFromSessions([session('a', 'Ann', '2026-11-07T09:50:00Z', null)], NOW);
    expect(ann).toMatchObject({ minutes: 10, stillHere: true, lastLeaveAt: null });
  });

  it('skips a session whose join never arrived from the time sums', () => {
    const [ann] = peopleFromSessions([session('a', 'Ann', null, '2026-11-07T09:50:00Z')], NOW);
    expect(ann).toMatchObject({ minutes: 0, firstJoinAt: null });
  });
});

describe('formatMinutes', () => {
  it('reads like a person would say it', () => {
    expect(formatMinutes(0)).toBe('0 min');
    expect(formatMinutes(59)).toBe('59 min');
    expect(formatMinutes(60)).toBe('1 h');
    expect(formatMinutes(95)).toBe('1 h 35 min');
  });
});

describe('orderEvents', () => {
  it('reads as it happened when LiveKit whole seconds tie or precede a knock in the same second', () => {
    const ordered = orderEvents([
      event('joined', { id: 'j', at: '2026-11-07T09:00:05.000Z' }),
      event('knocked', { id: 'k', at: '2026-11-07T09:00:05.300Z' }),
      event('left', { id: 'l', at: '2026-11-07T09:00:05.900Z' }),
      event('admitted', { id: 'a', at: '2026-11-07T09:00:05.600Z' }),
      event('joined', { id: 'early', at: '2026-11-07T09:00:01.000Z' }),
    ]);
    expect(ordered.map((e) => e.id)).toEqual(['early', 'k', 'a', 'j', 'l']);
  });
});
