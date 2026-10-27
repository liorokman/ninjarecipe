// Starts the UI harness, then runs unit + browser specs. Usage: node run.mjs [--headed] [--grep pattern] [--real]
import { spawn, spawnSync } from 'node:child_process';
import { readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const args = process.argv.slice(2);
const grep = args.includes('--grep') ? args[args.indexOf('--grep') + 1] : null;
if (args.includes('--headed')) process.env.HEADED = '1';
const chrome = process.env.CHROME_PATH || '/usr/bin/google-chrome';
if (!existsSync(chrome) && !process.env.CHROME_PATH) {
  console.error(`Chrome not found at ${chrome}. Install Google Chrome or set CHROME_PATH.`); process.exit(2);
}

const hargs = args.includes('--real') ? [] : ['-fixtures'];
const bin = path.join(root, 'bin', 'uiharness');
const cmd = existsSync(bin) && !process.env.UI_GO_RUN ? [bin, [...hargs, '-web', './web']] : ['go', ['run', './test/uiharness', ...hargs, '-web', './web']];
const server = spawn(cmd[0], cmd[1], { cwd: root, stdio: ['ignore', 'pipe', 'inherit'] });
const info = await new Promise((resolve, reject) => {
  let buf = '';
  server.stdout.on('data', (d) => { buf += d; const nl = buf.indexOf('\n'); if (nl >= 0) { try { resolve(JSON.parse(buf.slice(0, nl))); } catch (e) { reject(e); } } });
  server.on('exit', (c) => reject(new Error(`harness exited ${c}`)));
  setTimeout(() => reject(new Error('harness start timeout')), 120000);
}).catch((e) => { console.error(e.message); server.kill(); process.exit(2); });

const env = { ...process.env, UI_BASE: info.baseURL, UI_MODE: info.mode || (args.includes('--real') ? 'real' : 'fixtures'), UI_USERS: JSON.stringify(info.users), UI_RECIPES: JSON.stringify(info.recipes) };
const specs = readdirSync(path.join(here, 'specs')).filter((f) => f.endsWith('.spec.mjs')).map((f) => path.join('specs', f));
const nargs = ['--test', '--test-concurrency=1', ...(grep ? ['--test-name-pattern', grep] : []), ...specs];
const r = spawnSync('node', nargs, { cwd: here, env, stdio: 'inherit' });
server.kill();
process.exit(r.status ?? 1);
