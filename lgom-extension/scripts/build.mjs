import { cpSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';

const ROOT = resolve(import.meta.dirname, '..');
const PLATFORM = process.argv[2] ?? 'chrome';

const OUT = join(ROOT, 'build', PLATFORM);
// Vite output goes to a scratch directory so the shared artifacts it leaves
// behind (single tailwind stylesheet, manifest-agnostic chunks) never mix into
// build/<platform>/.
const STAGE = join(tmpdir(), `lgom-extension-${PLATFORM}`);

rmSync(OUT, { recursive: true, force: true });
rmSync(STAGE, { recursive: true, force: true });
mkdirSync(OUT, { recursive: true });

execFileSync('npx', ['vite', 'build', '--outDir', STAGE, '--emptyOutDir'], {
	cwd: ROOT,
	stdio: 'inherit',
	env: { ...process.env, BUILD_TARGET: PLATFORM }
});

// Move the platform's own artifacts out of the staging directory.
for (const name of ['popup.js', 'options.js', 'background.js']) {
	moveIfExists(join(STAGE, name), join(OUT, name));
}
moveIfExists(join(STAGE, 'chunks'), join(OUT, 'chunks'));

// The two UI surfaces import the same tailwind stylesheet, so Vite emits one
// shared CSS file; each surface gets a copy under its own name.
const emittedCss = readdirSync(STAGE).filter(
	(file) => file.endsWith('.css') && statSync(join(STAGE, file)).isFile()
);
for (const entry of ['popup', 'options']) {
	const css = emittedCss[0];
	if (css === undefined) continue;
	cpSync(join(STAGE, css), join(OUT, `${entry}.css`));
}

// The platform directory contributes only its manifest; its sources are
// already bundled into background.js by the entries above.
cpSync(join(ROOT, `src/platform/${PLATFORM}/manifest.json`), join(OUT, 'manifest.json'));
cpSync(join(ROOT, `src/popup/index.html`), join(OUT, 'popup.html'));
cpSync(join(ROOT, `src/options/index.html`), join(OUT, 'options.html'));
cpSync(join(ROOT, 'src/static'), OUT, { recursive: true });

// Remove the vite-emitted artifacts; everything the platform needs has been
// copied out of the staging directory.
rmSync(STAGE, { recursive: true, force: true });

console.log(`built ${PLATFORM} -> ${OUT}`);

/**
 * @param {string} from
 * @param {string} to
 */
function moveIfExists(from, to) {
	try {
		statSync(from);
	} catch {
		return;
	}
	mkdirSync(join(to, '..'), { recursive: true });
	cpSync(from, to, { recursive: true });
	rmSync(from, { recursive: true, force: true });
}
