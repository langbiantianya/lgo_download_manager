<script>
	import { getRecent } from '$lib/recent.js';
	import { i18nGetMessage } from '$lib/platform-api.js';
	import { handoffChannelLabel } from '$lib/channels.js';

	let items = $state(/** @type {import('$lib/types.js').RecentDownload[]} */ ([]));

	$effect(() => {
		getRecent().then((loaded) => {
			items = loaded.slice(0, 8);
		});
	});
</script>

<section class="rounded bg-neutral-800 p-3 text-xs">
	<h2 class="mb-2 font-medium">{i18nGetMessage('recent')}</h2>
	{#if items.length === 0}
		<p class="text-neutral-400">{i18nGetMessage('noInterceptionsYet')}</p>
	{:else}
		<ul class="flex flex-col gap-1">
			{#each items as item (item.timestamp + item.url)}
				{@const channel = handoffChannelLabel(item.reason)}
				<li class="flex items-center gap-2">
					<span class={item.ok ? 'text-green-400' : 'text-neutral-500'}>
						{item.ok ? i18nGetMessage('sent') : i18nGetMessage('skipped')}
					</span>
					{#if channel !== null}
						<span class="rounded bg-neutral-700 px-1 text-[10px] text-neutral-300">
							{i18nGetMessage(channel)}
						</span>
					{/if}
					<span class="truncate" title={item.url}>{item.name || item.url}</span>
				</li>
			{/each}
		</ul>
	{/if}
</section>
