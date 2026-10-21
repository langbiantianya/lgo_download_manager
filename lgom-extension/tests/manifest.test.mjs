import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

import { LOCAL_PORT_CANDIDATES } from '../src/lib/constants.js';

// tests/ 的上一级就是扩展根目录。
const ROOT = new URL('../', import.meta.url);

/**
 * @param {string} platform
 */
async function readManifest(platform) {
	const path = new URL(`src/platform/${platform}/manifest.json`, ROOT);
	return JSON.parse(await readFile(path, 'utf8'));
}

/**
 * @param {string} locale
 */
async function readMessages(locale) {
	const path = new URL(`src/static/_locales/${locale}/messages.json`, ROOT);
	return JSON.parse(await readFile(path, 'utf8'));
}

describe('manifest', () => {
	it('两个平台都是 MV3', async () => {
		const chrome = await readManifest('chrome');
		const firefox = await readManifest('firefox');

		assert.equal(chrome.manifest_version, 3);
		assert.equal(firefox.manifest_version, 3);
	});

	it('两个平台声明的权限一致', async () => {
		const chrome = await readManifest('chrome');
		const firefox = await readManifest('firefox');

		assert.deepEqual([...chrome.permissions].sort(), [...firefox.permissions].sort());
		assert.deepEqual([...chrome.host_permissions].sort(), [...firefox.host_permissions].sort());
	});

	it('代码用到的权限都在 manifest 里', async () => {
		for (const platform of ['chrome', 'firefox']) {
			const manifest = await readManifest(platform);

			// downloads: onCreated / cancel / erase
			assert.ok(manifest.permissions.includes('downloads'), `${platform}: downloads`);
			// storage: 配置、最近日志、请求头缓存镜像、端口缓存
			assert.ok(manifest.permissions.includes('storage'), `${platform}: storage`);
			// tabs: lgom:// 回退通道要开/复用转发标签页
			assert.ok(manifest.permissions.includes('tabs'), `${platform}: tabs`);
			// webRequest: 观察请求头以供交接
			assert.ok(manifest.permissions.includes('webRequest'), `${platform}: webRequest`);
			// 端口通道要能访问本机回环地址
			assert.ok(
				manifest.host_permissions.includes('http://127.0.0.1/*'),
				`${platform}: 缺 127.0.0.1 主机权限`
			);
		}
	});

	it('不需要 scripting 与 webRequestBlocking', async () => {
		for (const platform of ['chrome', 'firefox']) {
			const manifest = await readManifest(platform);

			assert.equal(manifest.permissions.includes('scripting'), false, `${platform}: scripting`);
			assert.equal(
				manifest.permissions.includes('webRequestBlocking'),
				false,
				`${platform}: webRequestBlocking`
			);
		}
	});

	it('后台以 ES 模块方式加载', async () => {
		const chrome = await readManifest('chrome');
		const firefox = await readManifest('firefox');

		// Chrome 用 Service Worker，Firefox 用事件页面，但两边都必须是模块。
		assert.equal(chrome.background.type, 'module');
		assert.equal(firefox.background.type, 'module');
		assert.equal(chrome.background.service_worker, 'background.js');
		assert.deepEqual(firefox.background.scripts, ['background.js']);
		assert.equal(chrome.background.scripts, undefined);
	});

	it('版本号两个平台一致', async () => {
		const chrome = await readManifest('chrome');
		const firefox = await readManifest('firefox');

		assert.equal(chrome.version, firefox.version);
	});
});

describe('国际化', () => {
	it('zh_CN 的键与 en 完全对齐', async () => {
		const en = await readMessages('en');
		const zh = await readMessages('zh_CN');

		assert.deepEqual(Object.keys(zh).sort(), Object.keys(en).sort());
	});

	it('每条文案都有 message', async () => {
		for (const locale of ['en', 'zh_CN']) {
			const messages = await readMessages(locale);

			for (const [key, entry] of Object.entries(messages)) {
				assert.equal(typeof entry.message, 'string', `${locale}/${key}`);
				assert.notEqual(entry.message, '', `${locale}/${key}`);
			}
		}
	});

	it('manifest 里的 __MSG_ 占位符都有对应文案', async () => {
		for (const platform of ['chrome', 'firefox']) {
			const manifest = await readManifest(platform);
			const messages = await readMessages('en');

			for (const field of ['name', 'description']) {
				const value = manifest[field];
				const match = /^__MSG_(.+)__$/.exec(String(value));
				if (!match) continue;
				assert.ok(messages[match[1]], `${platform}: ${field} 引用了不存在的文案 ${match[1]}`);
			}
		}
	});

	it('default_locale 指向存在的目录', async () => {
		const manifest = await readManifest('chrome');
		const messages = await readMessages(manifest.default_locale);

		assert.ok(Object.keys(messages).length > 0);
	});
});

describe('与桌面客户端的协议契约', () => {
	it('候选端口与 Go 侧 PortCandidates 一致', async (t) => {
		const path = new URL('../internal/handoff/handoff.go', ROOT);
		let source;
		try {
			source = await readFile(path, 'utf8');
		} catch {
			t.skip('找不到 internal/handoff/handoff.go（扩展目录被单独拷走了）');
			return;
		}

		const match = /var PortCandidates = \[\]int\{([^}]*)\}/.exec(source);
		assert.ok(match, '没在 handoff.go 里找到 PortCandidates');

		const goPorts = match[1]
			.split(',')
			.map((part) => Number(part.trim()))
			.filter((port) => Number.isFinite(port));

		assert.deepEqual(LOCAL_PORT_CANDIDATES, goPorts);
	});
});
