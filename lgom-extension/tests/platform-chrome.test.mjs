import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { installDeadClientStub, installLiveClientStub } from './helpers/browser-stub.mjs';
import { createFlow, runDownload } from './helpers/flow-harness.mjs';

const URL_UNDER_TEST = 'https://example.com/files/a.zip';
const FILE_NAME = 'a.zip';

describe('Chrome 平台的拦截流程', () => {
	it('浏览器启动时恢复的积压下载不会转发给客户端', async () => {
		const stub = await createFlow('chrome');
		const client = installLiveClientStub();
		const previousSessionStart = new Date(Date.now() - 60_000).toISOString();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME,
				startTime: previousSessionStart
			});

			assert.deepEqual(calls.cancelled, [], '不该取消浏览器恢复的下载');
			assert.deepEqual(calls.erased, [], '不该擦除浏览器恢复的下载');
			assert.deepEqual(calls.tabsCreated, [], '不该为浏览器恢复的下载开标签页');
			assert.deepEqual(recent, [], '恢复旧下载不算一次新的拦截');
		} finally {
			client.restore();
		}
	});

	it('客户端在跑时走端口通道，不开标签页', async () => {
		const stub = await createFlow('chrome');
		const client = installLiveClientStub();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME,
				startTime: new Date().toISOString()
			});

			assert.deepEqual(calls.cancelled, [7], '浏览器下载应被取消');
			assert.deepEqual(calls.erased, [{ id: 7 }], '浏览器下载应被擦除');
			assert.equal(recent[0]?.ok, true);
			assert.equal(recent[0]?.reason, 'forwarded-port');
			assert.equal(recent[0]?.url, URL_UNDER_TEST);
			assert.equal(recent[0]?.name, FILE_NAME);
			assert.deepEqual(calls.tabsCreated, [], '端口通道不该开标签页');
			assert.deepEqual(calls.tabsUpdated, [], '端口通道不该导航标签页');
		} finally {
			client.restore();
		}
	});

	it('客户端没在跑时退到 lgom:// 标签页', async () => {
		const stub = await createFlow('chrome');
		const client = installDeadClientStub();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME
			});

			assert.equal(recent[0]?.ok, true);
			assert.equal(recent[0]?.reason, 'forwarded-tab');
			assert.equal(calls.tabsCreated.length, 1, '应该开一个转发标签页');
			assert.ok(
				String(calls.tabsCreated[0]?.url).endsWith('handoff.html'),
				JSON.stringify(calls.tabsCreated)
			);
			assert.equal(calls.tabsUpdated.length, 1, '应该把标签页导航到 lgom://');
			assert.ok(
				String(calls.tabsUpdated[0]?.url).startsWith('lgom://download?'),
				String(calls.tabsUpdated[0]?.url)
			);
			assert.ok(
				String(calls.tabsUpdated[0]?.url).includes(encodeURIComponent(URL_UNDER_TEST)),
				String(calls.tabsUpdated[0]?.url)
			);
		} finally {
			client.restore();
		}
	});

	it('拦截开关关掉时完全不动浏览器的下载', async () => {
		const stub = await createFlow('chrome');
		const client = installLiveClientStub();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME,
				config: { enabled: false }
			});

			assert.deepEqual(calls.cancelled, [], '不该取消下载');
			assert.deepEqual(calls.erased, [], '不该擦除下载');
			assert.deepEqual(calls.tabsCreated, [], '不该开标签页');
			// 开关关着时平台是静默的：连最近日志都不写（用户主动关的，不是拦截失败）。
			assert.deepEqual(recent, []);
		} finally {
			client.restore();
		}
	});

	it('URL 不在白名单里时跳过', async () => {
		const stub = await createFlow('chrome');
		const client = installLiveClientStub();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME,
				config: { enabled: true, mode: 'whitelist', patterns: ['\\.iso$'] }
			});

			assert.deepEqual(calls.cancelled, [], '不该取消下载');
			assert.equal(recent[0]?.ok, false);
			assert.equal(recent[0]?.reason, 'skipped');
		} finally {
			client.restore();
		}
	});
});
