import type { CapacitorConfig } from '@capacitor/cli';

// The app is a native shell around the TeamUp web app of one server. It loads
// that server's pages rather than a copy built into the app, so whatever is
// deployed to the server is what the app shows: the app and the website can
// never drift apart, and only changes to the shell itself need a store release.
//
// Which server is chosen when the app is built, with TEAMUP_APP_URL (see
// README.md). The store id and the name under the icon live in the native
// projects, because they never change once an app is published.
const url = process.env.TEAMUP_APP_URL?.replace(/\/+$/, '');
if (!url || !/^https:\/\/[^/]+$/.test(url)) {
  throw new Error('Set TEAMUP_APP_URL to the server, like https://teamup.example.com (https, no path).');
}

const config: CapacitorConfig = {
  appId: 'sh.teamup.app',
  appName: 'TeamUp',
  webDir: 'www',
  // Lets the server tell the app from a browser, in its logs and anywhere else.
  appendUserAgent: 'TeamUpApp',
  backgroundColor: '#ffffff',
  server: {
    url,
    // Shown when the server cannot be reached, instead of a browser error page.
    errorPath: 'offline.html',
  },
  android: {
    // Calls and uploads must never fall back to plain http.
    allowMixedContent: false,
  },
  plugins: {
    // The web app already pads itself with env(safe-area-inset-*) and asks for
    // viewport-fit=cover, so it is drawn edge to edge exactly as in a browser.
    SystemBars: { insetsHandling: 'native', initialViewportFitValueHint: 'cover' },
  },
};

export default config;
