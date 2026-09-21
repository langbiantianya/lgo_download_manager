<script>
	import { getConfig, setConfig } from '$lib/storage.js';
	import { i18nGetMessage } from '$lib/platform-api.js';

	let enabled = $state(true);
	let saving = $state(false);

	$effect(() => {
		getConfig().then((config) => {
			enabled = config.enabled;
		});
	});

	async function toggle() {
		saving = true;
		try {
			enabled = !enabled;
			await setConfig({ enabled });
		} finally {
			saving = false;
		}
	}
</script>

<section class="rounded bg-neutral-800 p-3">
	<label class="flex items-center justify-between gap-2 text-xs">
		<span>{i18nGetMessage('interceptDownloadsLabel')}</span>
		<input
			type="checkbox"
			checked={enabled}
			disabled={saving}
			onchange={toggle}
			class="h-4 w-4 accent-green-500"
		/>
	</label>
</section>
