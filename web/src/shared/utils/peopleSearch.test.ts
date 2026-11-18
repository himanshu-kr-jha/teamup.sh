import { describe, expect, it } from 'vitest';
import { byName, fold, indexBy, matches, personFacts, rankBy, search, teamFacts, type PersonLike } from './peopleSearch';

const people: Array<PersonLike & { id: string }> = [
  { id: 'sam', name: 'Sam Mia', email: 'sam@nimbus.example', jobTitle: 'Designer', teams: ['Platform'] },
  { id: 'mia', name: 'Mia Lee', email: 'mia@nimbus.example', jobTitle: 'AI/ML Engineer', teams: ['Field Service'] },
  { id: 'tamia', name: 'Tamia Ray', email: 'tamia@nimbus.example', jobTitle: '', teams: [] },
  { id: 'jose', name: 'José Kumar', email: 'jk@nimbus.example', jobTitle: 'Accountant', teams: ['Finance'] },
  { id: 'max', name: 'Max Doe', email: 'miacontact@nimbus.example', jobTitle: 'Sales', teams: [] },
];
const ids = (list: Array<{ id: string }>) => list.map((p) => p.id);
const find = (query: string) => ids(rankBy(people, query, (p) => personFacts(p)));

describe('finding people', () => {
  it('puts a name that starts with the word first, then a word of the name, then anything else', () => {
    // Mia Lee by her first name; Sam Mia by his second; Max by his email; Tamia only contains it.
    expect(find('mia')).toEqual(['mia', 'sam', 'max', 'tamia']);
  });

  it('ignores case, accents and surrounding space', () => {
    expect(find('  JOSE ')).toEqual(['jose']);
    expect(find('kumár')).toEqual(['jose']);
    expect(fold('Ångström Ñoño')).toBe('angstrom nono');
  });

  it('finds by email, company role and team', () => {
    expect(find('jk@nimbus')).toEqual(['jose']);
    expect(find('ml engineer')).toEqual(['mia']);
    expect(find('accountant')).toEqual(['jose']);
    expect(find('field serv')).toEqual(['mia']);
    expect(find('platform')).toEqual(['sam']);
  });

  it('needs every word to match, in any order', () => {
    expect(find('lee mia')).toEqual(['mia']);
    expect(find('mia sales')).toEqual(['max']);
    expect(find('mia nobody')).toEqual([]);
  });

  it('keeps the order given for an empty query, and among equal matches', () => {
    expect(find('')).toEqual(ids(people));
    expect(find('   ')).toEqual(ids(people));
    // All five have the same email domain: a tie, so the order given stands.
    expect(find('nimbus')).toEqual(ids(people));
  });

  it('matches a handle as well as a name', () => {
    const withHandle = [{ id: 'a', name: 'Alice Archer', aliases: ['alice.a'] }];
    expect(ids(rankBy(withHandle, 'alice.a', (p) => personFacts(p)))).toEqual(['a']);
  });

  it('says whether one person matches', () => {
    expect(matches(personFacts(people[1]), 'engineer')).toBe(true);
    expect(matches(personFacts(people[1]), 'designer')).toBe(false);
    expect(matches(personFacts(people[1]), '')).toBe(true);
  });

  it('sorts by name ignoring case and accents', () => {
    const list = [{ name: 'zoe' }, { name: 'Émile' }, { name: 'adam' }];
    expect([...list].sort(byName).map((p) => p.name)).toEqual(['adam', 'Émile', 'zoe']);
  });
});

describe('finding teams', () => {
  const teams = [
    { id: 'p', name: 'Platform', members: ['Sam Mia', 'Ravi Shah'] },
    { id: 'f', name: 'Field Service', members: ['Mia Lee'] },
    { id: 'pf', name: 'Payments Platform', members: [] },
  ];

  it('finds by the team name first, then by who is on it', () => {
    expect(ids(rankBy(teams, 'platform', (t) => teamFacts(t)))).toEqual(['p', 'pf']);
    expect(ids(rankBy(teams, 'ravi', (t) => teamFacts(t)))).toEqual(['p']);
    expect(ids(rankBy(teams, 'mia', (t) => teamFacts(t)))).toEqual(['p', 'f']);
  });
});

describe('a large organization', () => {
  it('searches 2000 people quickly once indexed', () => {
    const many = Array.from({ length: 2000 }, (_, i) => ({
      id: String(i),
      name: `Person ${i} Example`,
      email: `person${i}@nimbus.example`,
      jobTitle: i % 2 ? 'Engineer' : 'Field technician',
      teams: [`Team ${i % 40}`],
    }));
    const index = indexBy(many, personFacts);
    const started = performance.now();
    for (const q of ['p', 'pe', 'per', 'pers', 'person 1', 'person 19', 'person 199', 'engineer team 3']) search(index, q);
    expect(performance.now() - started).toBeLessThan(250);
    expect(search(index, 'person 1999').map((p) => p.id)[0]).toBe('1999');
  });
});
