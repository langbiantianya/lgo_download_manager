<script>
	import { getPlatformName, i18nGetMessage } from '$lib/platform-api.js';
	import StatusPanel from './components/StatusPanel.svelte';
	import InterceptToggle from './components/InterceptToggle.svelte';
	import RecentDownloads from './components/RecentDownloads.svelte';

	let platform = $state(getPlatformName());
</script>

<div class="flex w-[22rem] flex-col gap-3 bg-neutral-900 p-4 text-neutral-100">
	<header class="flex items-center justify-between border-b border-neutral-700 pb-2">
		<h1 class="text-sm font-semibold">{i18nGetMessage('extensionName')}</h1>
		<span class="rounded bg-neutral-700 px-2 py-0.5 text-xs">{platform}</span>
	</header>

	<StatusPanel />
	<InterceptToggle />
	<RecentDownloads />
</div>
