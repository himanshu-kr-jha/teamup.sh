import { describe, expect, it } from 'vitest';
import { TYPING_FORGET_MS, applyTyping, forgetQuiet, typingText } from './typing';

describe('typingText', () => {
  it('names one, two, or two and how many more', () => {
    expect(typingText([])).toBe('');
    expect(typingText(['Alice'])).toBe('Alice is typing…');
    expect(typingText(['Alice', 'Bob'])).toBe('Alice and Bob are typing…');
    expect(typingText(['Alice', 'Bob', 'Cy'])).toBe('Alice, Bob and 1 other are typing…');
    expect(typingText(['Alice', 'Bob', 'Cy', 'Di'])).toBe('Alice, Bob and 2 others are typing…');
  });
});

describe('who is typing', () => {
  it('adds somebody who starts and takes off somebody who stops, per conversation', () => {
    let all = applyTyping({}, { channelId: 'c1', userId: 'alice', isTyping: true }, 0);
    all = applyTyping(all, { channelId: 'c2', userId: 'bob', isTyping: true }, 0);
    expect(Object.keys(all.c1)).toEqual(['alice']);
    expect(Object.keys(all.c2)).toEqual(['bob']);
    all = applyTyping(all, { channelId: 'c1', userId: 'alice', isTyping: false }, 0);
    expect(Object.keys(all.c1)).toEqual([]);
  });

  it('ignores a frame that does not say who or where', () => {
    const all = {};
    expect(applyTyping(all, { channelId: 'c1', isTyping: true }, 0)).toBe(all);
    expect(applyTyping(all, { userId: 'alice', isTyping: true }, 0)).toBe(all);
  });

  it('forgets somebody not heard from in time, and keeps somebody still typing', () => {
    let all = applyTyping({}, { channelId: 'c1', userId: 'alice', isTyping: true }, 0);
    all = applyTyping(all, { channelId: 'c1', userId: 'bob', isTyping: true }, 3_000);
    const later = forgetQuiet(all, TYPING_FORGET_MS + 1);
    expect(Object.keys(later.c1)).toEqual(['bob']);
    expect(forgetQuiet(later, TYPING_FORGET_MS + 2)).toBe(later);
    expect(forgetQuiet(later, 3_000 + TYPING_FORGET_MS + 1)).toEqual({});
  });
});
