<script>
	import { getConfig } from '$lib/storage.js';
	import { DEFAULT_CONFIG } from '$lib/constants.js';
	import { i18nGetMessage } from '$lib/platform-api.js';

	let config = $state(
		/** @type {import('$lib/types.js').InterceptConfig} */ ({ ...DEFAULT_CONFIG })
	);
	let loading = $state(true);

	$effect(() => {
		getConfig().then((loaded) => {
			config = loaded;
			loading = false;
		});
	});

	const MODE_LABELS = {
		all: 'modeAll',
		whitelist: 'modeWhitelist',
		blacklist: 'modeBlacklist'
	};
</script>

<section class="rounded bg-neutral-800 p-3 text-xs">
	{#if loading}
		<p class="text-neutral-400">{i18nGetMessage('loading')}</p>
	{:else}
		<dl class="grid grid-cols-2 gap-1">
			<dt class="text-neutral-400">{i18nGetMessage('status')}</dt>
			<dd class={config.enabled ? 'text-green-400' : 'text-red-400'}>
				{config.enabled ? i18nGetMessage('active') : i18nGetMessage('paused')}
			</dd>
			<dt class="text-neutral-400">{i18nGetMessage('mode')}</dt>
			<dd>{i18nGetMessage(MODE_LABELS[config.mode])}</dd>
			<dt class="text-neutral-400">{i18nGetMessage('patterns')}</dt>
			<dd>{config.patterns.length}</dd>
			<dt class="text-neutral-400">{i18nGetMessage('urlBudget')}</dt>
			<dd>{config.maxUrlLength}</dd>
		</dl>
	{/if}
</section>
