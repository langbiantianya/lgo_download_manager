// 一个假的桌面客户端：照着 Go 侧 internal/handoff 的行为实现 /ping 与
// /download，让扩展的端口通道能对着真实的 HTTP 跑一遍（而不是只对着桩）。
//
// 行为对齐 internal/handoff/handoff.go：
//   - 只放行扩展来源（空 Origin 放行，那是本机脚本）
//   - /download 必须带 X-LGOM-Client
//   - CORS 头回显来源，扩展的 fetch 才读得到响应体
import { createServer } from 'node:http';
import { connect } from 'node:net';

const APP_ID = 'lgo_download_manager';
const CLIENT_HEADER = 'x-lgom-client';

const EXTENSION_ORIGIN_PREFIXES = [
	'chrome-extension://',
	'moz-extension://',
	'extension://',
	'safari-web-extension://'
];

/**
 * @param {string} origin
 * @returns {boolean}
 */
export function originAllowed(origin) {
	if (!origin) return true;
	return EXTENSION_ORIGIN_PREFIXES.some((prefix) => origin.startsWith(prefix));
}

/**
 * 端口上有没有东西在听。
 *
 * @param {number} port
 * @param {number} [timeoutMs]
 * @returns {Promise<boolean>}
 */
export function portBusy(port, timeoutMs = 300) {
	return new Promise((resolve) => {
		const socket = connect({ host: '127.0.0.1', port });
		const done = (busy) => {
			socket.destroy();
			resolve(busy);
		};
		socket.setTimeout(timeoutMs);
		socket.once('connect', () => done(true));
		socket.once('timeout', () => done(false));
		socket.once('error', () => done(false));
	});
}

/**
 * 候选端口里有没有已经被占用的。扩展会并行探测全部候选、取最小的应答者，
 * 所以只要有一个候选被别的进程占着，测试里的假客户端就有可能被真实客户端
 * 抢走——这种情况必须跳过测试，而不是产生一条真实任务。
 *
 * @param {number[]} candidates
 * @returns {Promise<number[]>} 被占用的端口
 */
export async function busyCandidates(candidates) {
	const busy = [];
	for (const port of candidates) {
		if (await portBusy(port)) busy.push(port);
	}
	return busy;
}

/**
 * 起一个假客户端。
 *
 * @param {{ port: number, appId?: string }} options
 * @returns {Promise<{ port: number, requests: {method: string, url: string, origin: string, client: string, body: any}[], close: () => Promise<void> }>}
 */
export async function startFakeClient(options) {
	const appId = options.appId ?? APP_ID;

	/** @type {{method: string, url: string, origin: string, client: string, body: any}[]} */
	const requests = [];

	const server = createServer((req, res) => {
		const origin = req.headers.origin ?? '';
		const client = req.headers[CLIENT_HEADER] ?? '';
		const url = req.url ?? '';

		// CORS：回显扩展来源，扩展的 fetch 才读得到响应体。
		if (origin && originAllowed(origin)) {
			res.setHeader('Access-Control-Allow-Origin', origin);
			res.setHeader('Vary', 'Origin');
		}

		if (req.method === 'GET' && url === '/ping') {
			requests.push({ method: 'GET', url, origin, client, body: null });
			if (!originAllowed(origin)) {
				res.writeHead(403, { 'Content-Type': 'application/json' });
				res.end(JSON.stringify({ ok: false, error: 'origin not allowed' }));
				return;
			}
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ app: appId, api: 1, port: options.port, pid: 1, version: 'test' }));
			return;
		}

		if (req.method === 'OPTIONS' && url === '/download') {
			requests.push({ method: 'OPTIONS', url, origin, client, body: null });
			if (!originAllowed(origin)) {
				res.writeHead(403);
				res.end();
				return;
			}
			res.writeHead(204, {
				'Access-Control-Allow-Methods': 'POST, OPTIONS',
				'Access-Control-Allow-Headers': 'Content-Type, X-LGOM-Client',
				'Access-Control-Max-Age': '600'
			});
			res.end();
			return;
		}

		if (req.method === 'POST' && url === '/download') {
			let raw = '';
			req.on('data', (chunk) => {
				raw += chunk;
			});
			req.on('end', () => {
				/** @type {any} */
				let body;
				try {
					body = JSON.parse(raw);
				} catch {
					body = null;
				}
				requests.push({ method: 'POST', url, origin, client, body });

				if (!originAllowed(origin)) {
					res.writeHead(403, { 'Content-Type': 'application/json' });
					res.end(JSON.stringify({ ok: false, error: 'origin not allowed: ' + origin }));
					return;
				}
				if (!client) {
					res.writeHead(400, { 'Content-Type': 'application/json' });
					res.end(JSON.stringify({ ok: false, error: 'missing X-LGOM-Client' }));
					return;
				}
				if (!body || typeof body.url !== 'string' || body.url === '') {
					res.writeHead(400, { 'Content-Type': 'application/json' });
					res.end(JSON.stringify({ ok: false, error: 'missing url' }));
					return;
				}
				res.writeHead(200, { 'Content-Type': 'application/json' });
				res.end(JSON.stringify({ ok: true }));
			});
			return;
		}

		res.writeHead(404, { 'Content-Type': 'application/json' });
		res.end(JSON.stringify({ ok: false, error: 'not found' }));
	});

	await new Promise((resolve) => server.listen(options.port, '127.0.0.1', resolve));

	return {
		port: options.port,
		requests,
		close: () =>
			new Promise((resolve) => {
				server.close(() => resolve());
			})
	};
}
