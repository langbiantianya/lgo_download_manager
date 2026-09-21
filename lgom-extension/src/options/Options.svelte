<script>
	import { i18nGetMessage } from '$lib/platform-api.js';
	import ProtocolSettings from './components/ProtocolSettings.svelte';
	import FilterRules from './components/FilterRules.svelte';
	import AboutPanel from './components/AboutPanel.svelte';
</script>

<div class="mx-auto flex max-w-2xl flex-col gap-6 bg-neutral-900 p-6 text-neutral-100">
	<header class="border-b border-neutral-700 pb-3">
		<h1 class="text-lg font-semibold">{i18nGetMessage('extensionName')}</h1>
		<p class="text-xs text-neutral-400">{i18nGetMessage('extensionDescription')}</p>
	</header>

	<ProtocolSettings />
	<FilterRules />
	<AboutPanel />
</div>
