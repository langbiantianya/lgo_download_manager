<script>
	import { getConfig } from '$lib/storage.js';
	import { DEFAULT_CONFIG } from '$lib/constants.js';

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
</script>

<section class="rounded bg-neutral-800 p-3 text-xs">
	{#if loading}
		<p class="text-neutral-400">loading…</p>
	{:else}
		<dl class="grid grid-cols-2 gap-1">
			<dt class="text-neutral-400">status</dt>
			<dd class={config.enabled ? 'text-green-400' : 'text-red-400'}>
				{config.enabled ? 'active' : 'paused'}
			</dd>
			<dt class="text-neutral-400">mode</dt>
			<dd>{config.mode}</dd>
			<dt class="text-neutral-400">patterns</dt>
			<dd>{config.patterns.length}</dd>
			<dt class="text-neutral-400">url budget</dt>
			<dd>{config.maxUrlLength}</dd>
		</dl>
	{/if}
</section>
