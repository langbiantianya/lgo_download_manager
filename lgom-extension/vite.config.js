import tailwindcss from '@tailwindcss/vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';
import { join, resolve } from 'node:path';

const PLATFORM = process.env.BUILD_TARGET ?? 'chrome';
const ROOT = import.meta.dirname;

export default defineConfig({
	root: ROOT,
	plugins: [
		tailwindcss(),
		svelte({
			compilerOptions: {
				// Force runes mode for the project, except for libraries.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			}
		})
	],
	build: {
		outDir: 'build',
		emptyOutDir: false,
		target: 'es2022',
		rollupOptions: {
			input: {
				popup: join(ROOT, 'src/popup/main.js'),
				options: join(ROOT, 'src/options/main.js'),
				background: join(ROOT, `src/platform/${PLATFORM}/background.js`)
			},
			output: {
				// Flat names so the manifest can reference `popup.js` etc.
				// directly; `background` bundles to a single file for Firefox.
				entryFileNames: '[name].js',
				chunkFileNames: 'chunks/[name]-[hash].js',
				// One stylesheet per surface; scripts/build.mjs names them after
				// the entry that imports them.
				assetFileNames: '[name].[ext]'
			},
			preserveEntrySignatures: 'strict'
		}
	},
	resolve: {
		alias: {
			$lib: resolve(ROOT, 'src/lib'),
			$platform: resolve(ROOT, `src/platform/${PLATFORM}`)
		}
	}
});
