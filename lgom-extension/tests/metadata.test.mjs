import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { normalizeMetadata, shouldIntercept } from '../src/lib/metadata.js';

const FALLBACK_UA = 'fallback-ua/1.0';

describe('normalizeMetadata', () => {
	it('用浏览器给的文件名、请求头里的 UA 与 Cookie 组装元数据', () => {
		const meta = normalizeMetadata(
			'https://example.com/a.zip',
			'a.zip',
			{
				'User-Agent': 'Mozilla/5.0',
				Cookie: 'sid=abc',
				Referer: 'https://example.com/page'
			},
			FALLBACK_UA
		);

		assert.equal(meta.url, 'https://example.com/a.zip');
		assert.equal(meta.name, 'a.zip');
		assert.equal(meta.ua, 'Mozilla/5.0');
		assert.equal(meta.cookies, 'sid=abc');
		assert.equal(meta.referer, 'https://example.com/page');
		assert.ok(meta.headers.includes('User-Agent: Mozilla/5.0'));
		assert.ok(meta.headers.includes('Cookie: sid=abc'));
	});

	it('请求头里没有 UA 时退回兜底 UA', () => {
		const meta = normalizeMetadata('https://example.com/a.zip', 'a.zip', {}, FALLBACK_UA);

		assert.equal(meta.ua, FALLBACK_UA);
		assert.equal(meta.cookies, '');
	});

	it('文件名带路径时只取最后一段', () => {
		const meta = normalizeMetadata('https://example.com/a.zip', '/tmp/path/a.zip', {}, FALLBACK_UA);

		assert.equal(meta.name, 'a.zip');
	});

	it('没有文件名时从 URL 推导（去掉查询串）', () => {
		const meta = normalizeMetadata(
			'https://example.com/files/a.zip?token=1',
			undefined,
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, 'a.zip');
	});

	it('文件名里的百分号编码会被解码', () => {
		const meta = normalizeMetadata(
			'https://example.com/a.zip',
			'%E4%B8%AD%E6%96%87.zip',
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, '中文.zip');
	});

	it('解码失败时保留原样', () => {
		const meta = normalizeMetadata('https://example.com/a.zip', '100%.zip', {}, FALLBACK_UA);

		assert.equal(meta.name, '100%.zip');
	});
});

describe('shouldIntercept', () => {
	const url = 'https://example.com/files/a.zip';

	it('all 模式拦截一切', () => {
		assert.equal(
			shouldIntercept(url, { enabled: true, mode: 'all', patterns: [], maxUrlLength: 2000 }),
			true
		);
	});

	it('whitelist 模式只拦截匹配的', () => {
		const config = { enabled: true, mode: 'whitelist', patterns: ['\\.zip$'], maxUrlLength: 2000 };

		assert.equal(shouldIntercept(url, config), true);
		assert.equal(shouldIntercept('https://example.com/a.html', config), false);
	});

	it('blacklist 模式拦截除匹配项外的', () => {
		const config = { enabled: true, mode: 'blacklist', patterns: ['\\.zip$'], maxUrlLength: 2000 };

		assert.equal(shouldIntercept(url, config), false);
		assert.equal(shouldIntercept('https://example.com/a.html', config), true);
	});

	it('无效正则降级为子字符串匹配', () => {
		const config = {
			enabled: true,
			mode: 'whitelist',
			patterns: ['[unclosed'],
			maxUrlLength: 2000
		};

		assert.equal(shouldIntercept('https://example.com/[unclosed', config), true);
		assert.equal(shouldIntercept(url, config), false);
	});
});
