<script>
	import { getConfig, setConfig } from '$lib/storage.js';
	import { DEFAULT_CONFIG } from '$lib/constants.js';

	let enabled = $state(DEFAULT_CONFIG.enabled);
	let maxUrlLength = $state(DEFAULT_CONFIG.maxUrlLength);
	let saved = $state(false);

	$effect(() => {
		getConfig().then((config) => {
			enabled = config.enabled;
			maxUrlLength = config.maxUrlLength;
		});
	});

	async function save() {
		await setConfig({ enabled, maxUrlLength: clampLength(maxUrlLength) });
		saved = true;
		setTimeout(() => (saved = false), 1500);
	}

	/** @param {number} value */
	function clampLength(value) {
		return Math.max(200, Math.min(8000, Math.round(value) || DEFAULT_CONFIG.maxUrlLength));
	}
</script>

<section class="flex flex-col gap-3 rounded bg-neutral-800 p-4 text-sm">
	<h2 class="font-medium">protocol</h2>

	<label class="flex items-center gap-2">
		<input type="checkbox" bind:checked={enabled} class="h-4 w-4 accent-green-500" />
		<span>intercept downloads and forward to LGOM</span>
	</label>

	<label class="flex flex-col gap-1">
		<span class="text-xs text-neutral-400">
			maximum URL length forwarded (core params only above this)
		</span>
		<input
			type="number"
			bind:value={maxUrlLength}
			min="200"
			max="8000"
			class="w-32 rounded bg-neutral-700 px-2 py-1 text-sm"
		/>
	</label>

	<div class="flex items-center gap-2">
		<button
			onclick={save}
			class="rounded bg-green-600 px-3 py-1 text-sm font-medium hover:bg-green-500"
		>
			save
		</button>
		{#if saved}
			<span class="text-xs text-green-400">saved</span>
		{/if}
	</div>

	<p class="text-xs text-neutral-500">
		downloads are handed off through <code class="text-neutral-400">lgom://download?url=…</code>
	</p>
</section>
