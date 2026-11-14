// Writes the server address into the app's own offline page, so "Try again"
// knows where to go back to. Run by `npm run sync` before Capacitor copies www/.
import { writeFileSync } from 'node:fs';

const url = (process.env.TEAMUP_APP_URL ?? '').replace(/\/+$/, '');
if (!/^https:\/\/[^/]+$/.test(url)) {
  console.error('Set TEAMUP_APP_URL to the server, like https://teamup.example.com (https, no path).');
  process.exit(1);
}
writeFileSync(new URL('../www/server.js', import.meta.url), `window.TEAMUP_URL = ${JSON.stringify(url)};\n`);
