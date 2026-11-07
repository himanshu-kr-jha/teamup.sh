import http from 'node:http';
import net from 'node:net';
import { createHash } from 'node:crypto';
import type { AddressInfo } from 'node:net';

const MARKER = '<meta name="teamup-build" content="next">';

export interface SwapServer {
  /** The origin to open. localhost, so a browser treats it as secure and allows service workers. */
  url: string;
  /** What a deploy looks like from a browser: sockets drop, and the origin now serves a different build. */
  deploy(): void;
  close(): Promise<void>;
}

/**
 * Stands in front of the real server and can swap to a "new build" while browsers
 * are connected. Everything passes through untouched until deploy(); after that, the
 * HTML carries a marker and sw.js precaches that HTML under a new revision, which is
 * exactly the difference a browser sees after a real deploy: a sw.js that differs by
 * a byte. LiveKit is not behind this, the same way a deploy of the app leaves it up.
 */
export async function startSwapServer(upstream: string): Promise<SwapServer> {
  const target = new URL(upstream);
  let deployed = false;
  const sockets = new Set<net.Socket>();

  const rewrite = (path: string, contentType: string, body: Buffer): Buffer => {
    if (!deployed) return body;
    if (path === '/sw.js') {
      const before = body.toString('utf8');
      const revision = createHash('md5').update('next').digest('hex');
      const after = before.replace(/(url:"index\.html",revision:")[^"]*"/, `$1${revision}"`);
      if (after === before) throw new Error('sw.js does not precache index.html where this expects it; the swap would change nothing');
      return Buffer.from(after);
    }
    if (contentType.includes('text/html')) return Buffer.from(body.toString('utf8').replace('<head>', `<head>${MARKER}`));
    return body;
  };

  const server = http.createServer((req, res) => {
    const headers = { ...req.headers, host: target.host };
    delete headers['accept-encoding']; // So the bodies above can be read.
    const out = http.request({ host: target.hostname, port: target.port, method: req.method, path: req.url, headers }, (back) => {
      const chunks: Buffer[] = [];
      back.on('data', (c) => chunks.push(c));
      back.on('end', () => {
        const path = (req.url ?? '').split('?')[0];
        const body = rewrite(path, String(back.headers['content-type'] ?? ''), Buffer.concat(chunks));
        const replyHeaders = { ...back.headers, 'content-length': String(body.length) };
        delete replyHeaders['transfer-encoding'];
        res.writeHead(back.statusCode ?? 502, replyHeaders);
        res.end(body);
      });
    });
    out.on('error', () => res.destroy());
    req.pipe(out);
  });

  // The app's WebSocket.
  server.on('upgrade', (req, client, head) => {
    const back = net.connect(Number(target.port), target.hostname, () => {
      const lines = [`${req.method} ${req.url} HTTP/1.1`];
      for (let i = 0; i < req.rawHeaders.length; i += 2) lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`);
      back.write(lines.join('\r\n') + '\r\n\r\n');
      if (head.length) back.write(head);
      client.pipe(back).pipe(client);
    });
    sockets.add(client as net.Socket);
    sockets.add(back);
    const done = () => {
      client.destroy();
      back.destroy();
      sockets.delete(client as net.Socket);
      sockets.delete(back);
    };
    back.on('error', done);
    back.on('close', done);
    client.on('error', done);
    client.on('close', done);
  });

  await new Promise<void>((resolve) => server.listen(0, 'localhost', resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://localhost:${port}`,
    deploy() {
      deployed = true;
      for (const s of sockets) s.destroy();
    },
    close: () =>
      new Promise((resolve) => {
        for (const s of sockets) s.destroy();
        server.closeAllConnections();
        server.close(() => resolve());
      }),
  };
}
