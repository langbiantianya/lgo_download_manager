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

	it('只解码一次，避免编码过的路径分隔符在清洗后重新出现', () => {
		const meta = normalizeMetadata(
			'https://example.com/download',
			'%252E%252E%252Fevil.zip',
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, '%2E%2E%2Fevil.zip');
	});

	it('解码失败时保留原样', () => {
		const meta = normalizeMetadata('https://example.com/a.zip', '100%.zip', {}, FALLBACK_UA);

		assert.equal(meta.name, '100%.zip');
	});

	it('浏览器只给出泛化名字时优先使用 Content-Disposition', () => {
		const meta = normalizeMetadata(
			'https://example.com/download',
			'download',
			{
				'content-disposition': `attachment; filename*=UTF-8''%E5%8F%91%E5%B8%83.zip`
			},
			FALLBACK_UA
		);

		assert.equal(meta.name, '发布.zip');
	});

	it('从响应头的 quoted filename 中解析文件名', () => {
		const meta = normalizeMetadata(
			'https://example.com/download',
			undefined,
			{ 'Content-Disposition': 'attachment; filename="release build; final.zip"' },
			FALLBACK_UA
		);

		assert.equal(meta.name, 'release build; final.zip');
	});

	it('从查询参数中的文件名推导', () => {
		const meta = normalizeMetadata(
			'https://example.com/api/download?token=1&file=release-1.2.tar.xz',
			undefined,
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, 'release-1.2.tar.xz');
	});

	it('查询参数名大小写不敏感', () => {
		const meta = normalizeMetadata(
			'https://example.com/download?FileName=release.zip',
			undefined,
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, 'release.zip');
	});

	it('从查询参数里的 response-content-disposition 推导', () => {
		const disposition = encodeURIComponent(`attachment; filename*=UTF-8''report%20final.pdf`);
		const meta = normalizeMetadata(
			`https://example.com/download?response-content-disposition=${disposition}`,
			undefined,
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, 'report final.pdf');
	});

	it('路径中的百分号编码与 fragment 不会污染文件名', () => {
		const meta = normalizeMetadata(
			'https://example.com/files/%E4%B8%AD%E6%96%87.zip#section',
			undefined,
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, '中文.zip');
	});

	it('Windows 路径只保留文件名', () => {
		const meta = normalizeMetadata(
			'https://example.com/download',
			'C:\\Users\\me\\Downloads\\setup.exe',
			{},
			FALLBACK_UA
		);

		assert.equal(meta.name, 'setup.exe');
	});

	it('点开头的文件名不会被清成普通文件名', () => {
		const meta = normalizeMetadata('https://example.com/download', '.env', {}, FALLBACK_UA);

		assert.equal(meta.name, '.env');
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
