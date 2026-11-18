import { describe, expect, it } from 'vitest';
import {
  FIRST_JOIN,
  deviceLabel,
  deviceProblem,
  facesAndRest,
  parseJoinChoice,
  presentPeople,
  readJoinChoice,
  saveJoinChoice,
  whoIsHere,
} from './prejoin';

function memoryStorage() {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

describe('whoIsHere', () => {
  it('says nobody, one, two and three by name', () => {
    expect(whoIsHere([])).toBe('Nobody is here yet');
    expect(whoIsHere(['Asha Rao'])).toBe('Asha is here');
    expect(whoIsHere(['Asha Rao', 'Ravi Kumar'])).toBe('Asha and Ravi are here');
    expect(whoIsHere(['Asha', 'Ravi', 'Mohan'])).toBe('Asha, Ravi and Mohan are here');
  });

  it('counts the rest past three', () => {
    expect(whoIsHere(['Asha', 'Ravi', 'Mohan', 'Leela'])).toBe('Asha, Ravi and 2 more are here');
    expect(whoIsHere(['Asha', 'Ravi', 'Mohan', 'Leela', 'Kiran'])).toBe('Asha, Ravi and 3 more are here');
  });

  it('speaks of somebody with no name as somebody', () => {
    expect(whoIsHere([undefined])).toBe('Somebody is here');
    expect(whoIsHere(['  '])).toBe('Somebody is here');
  });
});

describe('presentPeople', () => {
  const t = '2026-11-18T10:00:00Z';
  it('is whoever came and has not left, without you', () => {
    const people = [
      { userId: 'a', userName: 'Asha', joinedAt: t },
      { userId: 'b', userName: 'Ravi', joinedAt: t, leftAt: t },
      { userId: 'c', userName: 'Mohan' },
      { userId: 'me', userName: 'Me', joinedAt: t },
    ];
    const { others, me } = presentPeople(people, 'me');
    expect(others.map((p) => p.userId)).toEqual(['a']);
    expect(me).toBe(true);
  });

  it('is nobody for a call with no list', () => {
    expect(presentPeople(undefined, 'me')).toEqual({ others: [], me: false });
  });
});

describe('facesAndRest', () => {
  it('draws everybody up to one past the limit, then counts the rest', () => {
    expect(facesAndRest([1, 2, 3, 4, 5], 4)).toEqual({ faces: [1, 2, 3, 4, 5], rest: 0 });
    expect(facesAndRest([1, 2, 3, 4, 5, 6, 7], 4)).toEqual({ faces: [1, 2, 3, 4], rest: 3 });
  });
});

describe('the remembered choice', () => {
  it('starts with both off for somebody who has never joined', () => {
    expect(readJoinChoice('u1', memoryStorage())).toEqual(FIRST_JOIN);
    expect(FIRST_JOIN).toEqual({ mic: false, camera: false });
  });

  it('keeps what was chosen, per person', () => {
    const storage = memoryStorage();
    saveJoinChoice('u1', { mic: true, camera: true, videoInput: 'cam-2' }, storage);
    saveJoinChoice('u1', { audioOutput: 'spk-1' }, storage);
    expect(readJoinChoice('u1', storage)).toEqual({ mic: true, camera: true, videoInput: 'cam-2', audioOutput: 'spk-1' });
    expect(readJoinChoice('u2', storage)).toEqual(FIRST_JOIN);
  });

  it('reads anything else as a first join', () => {
    expect(parseJoinChoice('not json')).toEqual(FIRST_JOIN);
    expect(parseJoinChoice('null')).toEqual(FIRST_JOIN);
    expect(parseJoinChoice('{"mic":"yes","camera":1,"videoInput":42}')).toEqual({
      mic: false,
      camera: false,
      audioInput: undefined,
      videoInput: undefined,
      audioOutput: undefined,
    });
  });

  it('carries on when storage refuses', () => {
    const refusing = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
    };
    expect(readJoinChoice('u1', refusing)).toEqual(FIRST_JOIN);
    expect(() => saveJoinChoice('u1', { mic: true }, refusing)).not.toThrow();
  });

  it('remembers nothing for nobody', () => {
    const storage = memoryStorage();
    saveJoinChoice(undefined, { mic: true }, storage);
    expect(storage.data.size).toBe(0);
  });
});

describe('devices', () => {
  it('tells a refusal from a missing device', () => {
    expect(deviceProblem({ name: 'NotAllowedError' })).toBe('denied');
    expect(deviceProblem({ name: 'NotFoundError' })).toBe('missing');
    expect(deviceProblem({ name: 'NotReadableError' })).toBe('busy');
    expect(deviceProblem(new Error('x'))).toBe('failed');
  });

  it('names a device the browser has not named yet', () => {
    expect(deviceLabel({ label: '' }, 0, 'audioinput')).toBe('Microphone 1');
    expect(deviceLabel({ label: '' }, 1, 'videoinput')).toBe('Camera 2');
    expect(deviceLabel({ label: 'Desk speakers' }, 0, 'audiooutput')).toBe('Desk speakers');
  });
});
