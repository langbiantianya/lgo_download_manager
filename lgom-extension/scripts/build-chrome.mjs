#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');

execFileSync('node', [join(root, 'scripts/build.mjs'), 'chrome'], {
	cwd: root,
	stdio: 'inherit'
});
