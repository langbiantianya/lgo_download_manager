<script>
	import { getPlatformName } from '$lib/platform-api.js';
	import { getConfig } from '$lib/storage.js';
	import { clearRecent } from '$lib/recent.js';
	import { MAX_FRAME_LEN } from '$lib/constants.js';
	import { i18nGetMessage } from '$lib/platform-api.js';

	let platform = $state(getPlatformName());
	let maxUrlLength = $state(0);

	$effect(() => {
		getConfig().then((config) => {
			maxUrlLength = config.maxUrlLength;
		});
	});

	async function clearLog() {
		await clearRecent();
	}
</script>

<section class="flex flex-col gap-2 rounded bg-neutral-800 p-4 text-xs text-neutral-400">
	<h2 class="text-sm font-medium text-neutral-200">{i18nGetMessage('about')}</h2>
	<p>
		{i18nGetMessage('aboutDescription', ['lgom://'])}
	</p>
	<dl class="grid grid-cols-2 gap-1">
		<dt>{i18nGetMessage('platform')}</dt>
		<dd>{platform}</dd>
		<dt>{i18nGetMessage('urlBudget')}</dt>
		<dd>{maxUrlLength || i18nGetMessage('default')}</dd>
		<dt>{i18nGetMessage('ipcFrameLimit')}</dt>
		<dd>{MAX_FRAME_LEN}</dd>
	</dl>
	<button
		onclick={clearLog}
		class="self-start rounded bg-neutral-700 px-3 py-1 text-xs hover:bg-neutral-600"
	>
		{i18nGetMessage('clearInterceptionLog')}
	</button>
</section>
