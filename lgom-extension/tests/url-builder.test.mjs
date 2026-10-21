import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
	buildLgomUrl,
	buildPortPayload,
	extractCookies,
	extractReferer,
	extractUserAgent,
	serializeForwardHeaders
} from '../src/lib/url-builder.js';

describe('buildPortPayload', () => {
	it('把元数据原样搬进 JSON 正文（不编码）', () => {
		const meta = {
			url: 'https://example.com/a b.zip?x=1&y=2',
			name: 'a b.zip',
			ua: 'Mozilla/5.0 (Windows NT 10.0)',
			headers: 'Referer: https://example.com/page',
			cookies: 'sid=abc',
			referer: 'https://example.com/page',
			timestamp: 1
		};

		const payload = buildPortPayload(meta);

		assert.deepEqual(payload, {
			url: meta.url,
			name: meta.name,
			ua: meta.ua,
			headers: meta.headers,
			cookies: meta.cookies
		});
		// 正文不编码：& 和空格都保持原样，客户端按 JSON 解析即可。
		assert.equal(payload.url, meta.url);
		assert.equal(payload.headers, meta.headers);
	});
});

describe('buildLgomUrl', () => {
	const meta = {
		url: 'https://example.com/a.zip',
		name: 'a.zip',
		ua: 'Mozilla/5.0',
		headers: 'Referer: https://example.com/page',
		cookies: 'sid=abc',
		referer: '',
		timestamp: 1
	};

	it('生成 lgom://download?… 并带上全部参数', () => {
		const url = buildLgomUrl(meta);

		assert.ok(url.startsWith('lgom://download?'), url);
		const params = new URL(url).searchParams;
		assert.equal(params.get('url'), meta.url);
		assert.equal(params.get('name'), meta.name);
		assert.equal(params.get('ua'), meta.ua);
		assert.equal(params.get('headers'), meta.headers);
		assert.equal(params.get('cookies'), meta.cookies);
	});

	it('URL 超预算时降级为核心参数（url/name/ua）', () => {
		const long = { ...meta, ua: 'u'.repeat(500) };
		const url = buildLgomUrl(long, 200);

		const params = new URL(url).searchParams;
		assert.equal(params.get('url'), long.url);
		assert.equal(params.get('ua'), long.ua);
		assert.equal(params.has('headers'), false);
		assert.equal(params.has('cookies'), false);
	});

	it('没超预算时保留完整参数', () => {
		const url = buildLgomUrl(meta, 2000);

		const params = new URL(url).searchParams;
		assert.equal(params.has('headers'), true);
		assert.equal(params.has('cookies'), true);
	});
});

describe('请求头提取', () => {
	it('cookie 大小写不敏感', () => {
		assert.equal(extractCookies({ Cookie: 'sid=abc' }), 'sid=abc');
		assert.equal(extractCookies({ cookie: 'sid=abc' }), 'sid=abc');
	});

	it('user-agent 与 referer 大小写不敏感', () => {
		assert.equal(extractUserAgent({ 'User-Agent': 'UA/1.0' }), 'UA/1.0');
		assert.equal(extractReferer({ Referer: 'https://example.com' }), 'https://example.com');
	});

	it('多个 cookie 用分号连接', () => {
		assert.equal(extractCookies({ cookie: ['a=1', 'b=2'] }), 'a=1; b=2');
	});

	it('缺失时返回空串', () => {
		assert.equal(extractCookies({}), '');
		assert.equal(extractUserAgent({}), '');
		assert.equal(extractReferer({}), '');
	});
});

describe('serializeForwardHeaders', () => {
	it('剥离逐跳与主机绑定头', () => {
		const serialized = serializeForwardHeaders({
			Host: 'example.com',
			Connection: 'keep-alive',
			'Content-Length': '42',
			'Accept-Encoding': 'gzip',
			Referer: 'https://example.com/page'
		});

		assert.equal(serialized, 'Referer: https://example.com/page');
	});

	it('同名的多个头用逗号连接', () => {
		const serialized = serializeForwardHeaders({ Accept: ['text/html', 'application/xml'] });

		assert.equal(serialized, 'Accept: text/html, application/xml');
	});

	it('值为 undefined 的头被跳过', () => {
		const serialized = serializeForwardHeaders({ Referer: undefined, Accept: 'x' });

		assert.equal(serialized, 'Accept: x');
	});
});
