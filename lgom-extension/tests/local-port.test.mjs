import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
	LOCAL_PORT_CACHE_KEY,
	LOCAL_PORT_CANDIDATES,
	LOCAL_PORT_CLIENT_HEADER
} from '../src/lib/constants.js';
import { createBrowserStub, installFetchStub } from './helpers/browser-stub.mjs';
import { busyCandidates, startFakeClient } from './helpers/fake-client.mjs';

// platform-api.js 在 import 时求值 globalThis.browser || globalThis.chrome，
// 桩必须先装好。
const stub = createBrowserStub();
globalThis.chrome = stub.api;

/**
 * 每个用例都要一个干净的 local-port 模块实例（它把端口记在模块级变量里），
// 用查询串打破 ESM 缓存。
 *
 * @param {number} caseId
 */
function freshLocalPort(caseId) {
	return import(`../src/lib/local-port.js?case=${caseId}`);
}

/**
 * 可编程的 fetch 桩：只有 alivePorts 上的端口会应答。
 *
 * @param {{ alivePorts?: number[], downloadStatus?: number, downloadBody?: unknown }} options
 */
function fakeFetch(options = {}) {
	const alivePorts = options.alivePorts ?? [];
	const downloadStatus = options.downloadStatus ?? 200;
	const downloadBody = options.downloadBody ?? { ok: true };

	/** @type {{url: string, init: any}[]} */
	const requests = [];

	const handler = async (url, init) => {
		requests.push({ url, init });
		const port = Number(new URL(url).port);

		if (url.endsWith('/ping')) {
			if (!alivePorts.includes(port)) throw new TypeError('fetch failed');
			return new Response(
				JSON.stringify({ app: 'lgo_download_manager', api: 1, port, pid: 1, version: 'test' }),
				{ status: 200, headers: { 'Content-Type': 'application/json' } }
			);
		}
		return new Response(JSON.stringify(downloadBody), {
			status: downloadStatus,
			headers: { 'Content-Type': 'application/json' }
		});
	};

	return { requests, restore: installFetchStub(handler) };
}

const PAYLOAD = {
	url: 'https://example.com/a.zip',
	name: 'a.zip',
	ua: 'Mozilla/5.0',
	headers: 'Referer: https://example.com/page',
	cookies: 'sid=abc'
};

describe('probeLocalPort', () => {
	it('探测到客户端的端口并写进 storage.session', async () => {
		stub.reset();
		const fetchStub = fakeFetch({ alivePorts: [17822] });

		try {
			const { probeLocalPort } = await freshLocalPort(1);
			const port = await probeLocalPort();

			assert.equal(port, 17822);
			assert.equal(stub.session.get(LOCAL_PORT_CACHE_KEY), 17822);
			assert.ok(
				fetchStub.requests.some((r) => r.url === 'http://127.0.0.1:17822/ping'),
				JSON.stringify(fetchStub.requests.map((r) => r.url))
			);
		} finally {
			fetchStub.restore();
		}
	});

	it('缓存的端口优先被试一次', async () => {
		stub.reset();
		stub.session.set(LOCAL_PORT_CACHE_KEY, 17823);
		const fetchStub = fakeFetch({ alivePorts: [17823, 17824] });

		try {
			const { probeLocalPort } = await freshLocalPort(2);
			const port = await probeLocalPort();

			assert.equal(port, 17823);
			assert.equal(
				fetchStub.requests[0].url,
				'http://127.0.0.1:17823/ping',
				'第一枪应该打缓存的端口'
			);
		} finally {
			fetchStub.restore();
		}
	});

	it('缓存的端口失效时重新在候选表里找', async () => {
		stub.reset();
		stub.session.set(LOCAL_PORT_CACHE_KEY, 17823);
		const fetchStub = fakeFetch({ alivePorts: [17824] });

		try {
			const { probeLocalPort } = await freshLocalPort(3);
			const port = await probeLocalPort();

			assert.equal(port, 17824);
			assert.equal(stub.session.get(LOCAL_PORT_CACHE_KEY), 17824);
		} finally {
			fetchStub.restore();
		}
	});
});

describe('sendToLocalPort', () => {
	it('客户端应答时交接成功', async () => {
		stub.reset();
		const fetchStub = fakeFetch({ alivePorts: [17821] });

		try {
			const { sendToLocalPort } = await freshLocalPort(4);
			const result = await sendToLocalPort(PAYLOAD);

			assert.equal(result.ok, true);
			assert.equal(result.port, 17821);

			const post = fetchStub.requests.find((r) => r.url.endsWith('/download'));
			assert.ok(post, '应该发出过 POST /download');
			assert.equal(post.init.headers[LOCAL_PORT_CLIENT_HEADER], 'extension');
			assert.equal(post.init.headers['Content-Type'], 'application/json');
			assert.deepEqual(JSON.parse(post.init.body), PAYLOAD);
		} finally {
			fetchStub.restore();
		}
	});

	it('客户端拒绝时返回 failed（不是 unreachable）', async () => {
		stub.reset();
		const fetchStub = fakeFetch({ alivePorts: [17821], downloadStatus: 403 });

		try {
			const { sendToLocalPort } = await freshLocalPort(5);
			const result = await sendToLocalPort(PAYLOAD);

			assert.equal(result.ok, false);
			assert.equal(result.kind, 'failed');
		} finally {
			fetchStub.restore();
		}
	});

	it('客户端没在跑时返回 unreachable', async () => {
		stub.reset();
		const fetchStub = fakeFetch({ alivePorts: [] });

		try {
			const { sendToLocalPort } = await freshLocalPort(6);
			const result = await sendToLocalPort(PAYLOAD);

			assert.equal(result.ok, false);
			assert.equal(result.kind, 'unreachable');
		} finally {
			fetchStub.restore();
		}
	});

	it('客户端报 5xx 时忘记缓存的端口', async () => {
		stub.reset();
		stub.session.set(LOCAL_PORT_CACHE_KEY, 17821);
		const fetchStub = fakeFetch({ alivePorts: [17821], downloadStatus: 500 });

		try {
			const { sendToLocalPort } = await freshLocalPort(7);
			const result = await sendToLocalPort(PAYLOAD);

			assert.equal(result.ok, false);
			assert.equal(result.kind, 'failed');
			assert.equal(stub.session.get(LOCAL_PORT_CACHE_KEY), undefined);
		} finally {
			fetchStub.restore();
		}
	});
});

describe('端口通道对着真实 HTTP', () => {
	it('假客户端能接住一次完整的交接', async (t) => {
		const busy = await busyCandidates(LOCAL_PORT_CANDIDATES);
		if (busy.length > 0) {
			t.skip(`候选端口被占用（${busy.join(', ')}），跳过：真客户端在场时测试会打到它身上`);
			return;
		}

		stub.reset();
		const client = await startFakeClient({ port: LOCAL_PORT_CANDIDATES[0] });

		try {
			const { probeLocalPort, sendToLocalPort } = await freshLocalPort(8);

			const port = await probeLocalPort();
			assert.equal(port, LOCAL_PORT_CANDIDATES[0]);

			const result = await sendToLocalPort(PAYLOAD);
			assert.equal(result.ok, true);

			const post = client.requests.find((r) => r.method === 'POST');
			assert.ok(post, '假客户端应该收到 POST');
			assert.equal(post.url, '/download');
			assert.equal(post.client, 'extension');
			assert.deepEqual(post.body, PAYLOAD);
		} finally {
			await client.close();
		}
	});
});
