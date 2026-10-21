import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { handoffChannelLabel, handoffReason } from '../src/lib/channels.js';

describe('handoffReason', () => {
	it('端口通道与标签页通道各有各的 reason', () => {
		assert.equal(handoffReason('port'), 'forwarded-port');
		assert.equal(handoffReason('tab'), 'forwarded-tab');
	});

	it('未知通道退回通用 reason', () => {
		assert.equal(handoffReason('unknown'), 'forwarded');
	});
});

describe('handoffChannelLabel', () => {
	it('两条现行通道各自对应一个徽标键', () => {
		assert.equal(handoffChannelLabel('forwarded-port'), 'channelPort');
		assert.equal(handoffChannelLabel('forwarded-tab'), 'channelTab');
	});

	it('历史 reason（旧原生消息主机）仍能显示成徽标', () => {
		assert.equal(handoffChannelLabel('forwarded-native'), 'channelNative');
		assert.equal(handoffChannelLabel('forwarded'), 'channelNative');
	});

	it('跳过与失败不是通道，不显示徽标', () => {
		assert.equal(handoffChannelLabel('skipped'), null);
		assert.equal(handoffChannelLabel('failed: boom'), null);
	});
});
