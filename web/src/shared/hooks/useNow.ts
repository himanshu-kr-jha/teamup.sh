import { useEffect, useState } from 'react';

/**
 * The time, looked at again every so often, for a page whose words depend on it:
 * a meeting turns missed at its end while its card is on screen.
 */
export function useNow(everyMs = 30_000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), everyMs);
    return () => clearInterval(timer);
  }, [everyMs]);
  return now;
}
