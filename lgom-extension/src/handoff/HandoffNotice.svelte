<script>
	import { i18nGetMessage } from '$lib/platform-api.js';
</script>

<div class="flex min-h-screen flex-col gap-3 bg-neutral-900 p-6 text-neutral-100">
	<h1 class="text-sm font-semibold">{i18nGetMessage('handoffPageTitle')}</h1>
	<p class="max-w-md text-xs text-neutral-400">{i18nGetMessage('handoffPageHint')}</p>
</div>
