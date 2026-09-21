<script>
	import { getConfig, setConfig } from '$lib/storage.js';
	import { DEFAULT_CONFIG } from '$lib/constants.js';

	const MODES = ['all', 'whitelist', 'blacklist'];

	let mode = $state(
		/** @type {import('$lib/types.js').InterceptConfig['mode']} */ (DEFAULT_CONFIG.mode)
	);
	let patterns = $state([...DEFAULT_CONFIG.patterns]);
	let draft = $state('');
	let saved = $state(false);

	$effect(() => {
		getConfig().then((config) => {
			mode = config.mode;
			patterns = [...config.patterns];
		});
	});

	function addPattern() {
		const value = draft.trim();
		if (!value) return;
		patterns = [...patterns, value];
		draft = '';
	}

	/** @param {number} index */
	function removePattern(index) {
		patterns = patterns.filter((_, i) => i !== index);
	}

	async function save() {
		await setConfig({ mode, patterns });
		saved = true;
		setTimeout(() => (saved = false), 1500);
	}
</script>

<section class="flex flex-col gap-3 rounded bg-neutral-800 p-4 text-sm">
	<h2 class="font-medium">filter rules</h2>

	<fieldset class="flex flex-col gap-1">
		<legend class="text-xs text-neutral-400">mode</legend>
		{#each MODES as option (option)}
			<label class="flex items-center gap-2">
				<input
					type="radio"
					name="mode"
					value={option}
					checked={mode === option}
					onclick={() =>
						(mode = /** @type {import('$lib/types.js').InterceptConfig['mode']} */ (option))}
					class="h-4 w-4 accent-green-500"
				/>
				<span>{option}</span>
			</label>
		{/each}
	</fieldset>

	{#if mode !== 'all'}
		<div class="flex flex-col gap-2">
			<div class="flex gap-2">
				<input
					bind:value={draft}
					onkeydown={(event) => event.key === 'Enter' && addPattern()}
					placeholder="regex or substring"
					class="flex-1 rounded bg-neutral-700 px-2 py-1 text-sm"
				/>
				<button
					onclick={addPattern}
					class="rounded bg-neutral-600 px-3 py-1 text-sm hover:bg-neutral-500"
				>
					add
				</button>
			</div>

			{#if patterns.length === 0}
				<p class="text-xs text-neutral-500">no patterns</p>
			{:else}
				<ul class="flex flex-col gap-1">
					{#each patterns as pattern, index (pattern + index)}
						<li class="flex items-center justify-between gap-2 rounded bg-neutral-700 px-2 py-1">
							<code class="truncate text-xs">{pattern}</code>
							<button
								onclick={() => removePattern(index)}
								class="text-xs text-red-400 hover:text-red-300"
							>
								remove
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}

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
</section>
