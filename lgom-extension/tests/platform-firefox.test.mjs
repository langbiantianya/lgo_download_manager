import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { installDeadClientStub, installLiveClientStub } from './helpers/browser-stub.mjs';
import { createFlow, runDownload } from './helpers/flow-harness.mjs';

const URL_UNDER_TEST = 'https://example.com/files/a.zip';
const FILE_NAME = 'a.zip';

describe('Firefox 平台的拦截流程', () => {
	it('客户端在跑时走端口通道，不开标签页', async () => {
		const stub = await createFlow('firefox');
		const client = installLiveClientStub();

		try {
			const { recent, calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME
			});

			assert.deepEqual(calls.cancelled, [7], '浏览器下载应被取消');
			assert.deepEqual(calls.erased, [{ id: 7 }], '浏览器下载应被擦除');
			assert.equal(recent[0]?.ok, true);
			assert.equal(recent[0]?.reason, 'forwarded-port');
			assert.deepEqual(calls.tabsCreated, [], '端口通道不该开标签页');
		} finally {
			client.restore();
		}
	});

	it('客户端没在跑时退到 lgom:// 标签页', async () => {
		const stub = await createFlow('firefox');
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
				String(calls.tabsCreated[0]?.url).startsWith('lgom://download?'),
				String(calls.tabsCreated[0]?.url)
			);
		} finally {
			client.restore();
		}
	});

	it('交接完成后转发标签页会被关掉', async () => {
		const stub = await createFlow('firefox');
		const client = installDeadClientStub();

		try {
			const { calls } = await runDownload(stub, {
				url: URL_UNDER_TEST,
				filename: FILE_NAME
			});

			const tabId = calls.tabsCreated[0]?.id ?? 4242;
			await new Promise((resolve) => setTimeout(resolve, 1200));

			assert.ok(calls.tabsRemoved.includes(tabId), JSON.stringify(calls.tabsRemoved));
		} finally {
			client.restore();
		}
	});
});
