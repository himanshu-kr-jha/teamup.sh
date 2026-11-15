/**
 * A time zone as a person says it: "India Standard Time (GMT+5:30)" rather than
 * an identifier. Identifiers also come in old and new spellings, and Chrome
 * reports India as Asia/Calcutta, a name the country stopped using in 2001.
 * Falls back to the identifier, made readable, where the browser has no name.
 */
export function zoneLabel(zone: string, at: Date = new Date(), locale = 'en'): string {
  const part = (style: 'long' | 'shortOffset') => {
    try {
      return new Intl.DateTimeFormat(locale, { timeZone: zone, timeZoneName: style })
        .formatToParts(at)
        .find((p) => p.type === 'timeZoneName')?.value;
    } catch {
      return undefined;
    }
  };
  const name = part('long');
  const offset = part('shortOffset');
  if (!name) return zone.replace(/_/g, ' ');
  // A zone with no name of its own is spelled as its offset, which needs no repeating.
  if (!offset || name.startsWith('GMT') || name === offset) return name;
  return `${name} (${offset})`;
}
