import { describe, expect, it } from 'vitest';
import { filtering, memberMatches } from './memberSearch';

const alice = { name: 'Alice Archer', email: 'alice@nimbus.example', jobTitle: 'AI/ML Engineer', teams: ['Platform', 'Field Service'], roles: ['member'] };
const jose = { name: 'José Kumar', email: 'jose@nimbus.example', jobTitle: '', teams: [], roles: ['manager'] };
const none = { query: '', team: '', role: '' };

describe('memberMatches', () => {
  it('finds by name, email, company role, team and role, in any order and case', () => {
    for (const query of ['alice', 'ARCHER', 'nimbus.example', 'ml engineer', 'field serv', 'member', 'engineer alice']) {
      expect(memberMatches(alice, { ...none, query }), query).toBe(true);
    }
    expect(memberMatches(alice, { ...none, query: 'alice manager' })).toBe(false);
  });

  it('ignores accents', () => {
    expect(memberMatches(jose, { ...none, query: 'jose' })).toBe(true);
  });

  it('narrows by team and role', () => {
    expect(memberMatches(alice, { ...none, team: 'Platform' })).toBe(true);
    expect(memberMatches(jose, { ...none, team: 'Platform' })).toBe(false);
    expect(memberMatches(jose, { ...none, role: 'manager' })).toBe(true);
    expect(memberMatches(alice, { ...none, role: 'manager', query: 'alice' })).toBe(false);
  });

  it('knows when nothing narrows the list', () => {
    expect(filtering(none)).toBe(false);
    expect(filtering({ ...none, query: '  ' })).toBe(false);
    expect(filtering({ ...none, team: 'Platform' })).toBe(true);
  });
});
