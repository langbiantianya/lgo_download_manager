#!/usr/bin/env node
// 打包入口:把 build/<platform>/ 组装成可直接分发的成品,输出到 dist/。
//
//   node scripts/package.mjs chrome   # lgom-extension-chrome-<版本>.{zip,crx}
//   node scripts/package.mjs firefox  # lgom-extension-firefox-<版本>.{zip,xpi}
//
// 选项:
//   --version=X.Y.Z   覆盖产物版本,并改写构建目录里的 manifest.json(release 传 tag 版本)
//   --key=<file.pem>  CRX 签名私钥;不给就在 dist/ 下新生成一把,此时扩展 ID 每次都变
//   --sign            提交 AMO 签名(需要 AMO_API_KEY / AMO_API_SECRET),产出签名 xpi
import { createHash, createPublicKey } from 'node:crypto';
import {
	copyFileSync,
	existsSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync
} from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';

const ROOT = resolve(import.meta.dirname, '..');
const DIST = join(ROOT, 'dist');
// web-ext 的 package.json 带 exports 字段,不能按子路径解析,直接拼 bin 路径。
const CRX3_BIN = join(ROOT, 'node_modules/crx3/bin/crx3.js');
const WEBEXT_BIN = join(ROOT, 'node_modules/web-ext/bin/web-ext.js');
// 清单版本号:1~4 段数字,Chrome 与 Firefox 都不接受预发布后缀。
const VERSION_RE = /^\d+(\.\d+){0,3}$/;
const USAGE =
	'用法: node scripts/package.mjs <chrome|firefox> [--version=X.Y.Z] [--key=file.pem] [--sign]';

const { platform, options } = parseArgv(process.argv.slice(2));

if (platform !== 'chrome' && platform !== 'firefox') fail(USAGE);
if (options.sign && platform !== 'firefox') fail('--sign 只对 firefox 有意义');

const outDir = join(ROOT, 'build', platform);
if (!existsSync(join(outDir, 'manifest.json'))) {
	fail(`缺少 ${relative(ROOT, outDir)}/manifest.json —— 先跑 npm run build:${platform}`);
}

const version = resolveVersion(outDir, options.version);
const base = `lgom-extension-${platform}-${version}`;
mkdirSync(DIST, { recursive: true });

const artifacts =
	platform === 'chrome'
		? packageChrome(outDir, base, options.key)
		: packageFirefox(outDir, base, options.sign);

console.log(`\n${base}`);
for (const artifact of artifacts) console.log(`  ${relative(ROOT, artifact)}`);

/**
 * @param {string[]} argv
 */
function parseArgv(argv) {
	/** @type {{ version?: string, key?: string, sign: boolean }} */
	const parsed = { sign: false };
	/** @type {string | undefined} */
	let target;
	for (const arg of argv) {
		if (arg.startsWith('--version=')) parsed.version = arg.slice('--version='.length);
		else if (arg.startsWith('--key=')) parsed.key = arg.slice('--key='.length);
		else if (arg === '--sign') parsed.sign = true;
		else if (target === undefined && !arg.startsWith('-')) target = arg;
		else fail(`${USAGE}\n未知参数: ${arg}`);
	}
	return { platform: target, options: parsed };
}

/**
 * 版本号以构建目录里的 manifest 为准;传了 --version 就把 manifest 改成它。
 * @param {string} dir
 * @param {string | undefined} override
 */
function resolveVersion(dir, override) {
	const manifestPath = join(dir, 'manifest.json');
	const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
	if (override === undefined) return manifest.version;
	if (!VERSION_RE.test(override)) {
		fail(`版本号 ${override} 不符合扩展清单要求(1~4 段数字,如 1.2.3)`);
	}
	if (manifest.version !== override) {
		manifest.version = override;
		writeFileSync(manifestPath, `${JSON.stringify(manifest, null, '\t')}\n`);
		console.log(`${relative(ROOT, manifestPath)}: 版本 -> ${override}`);
	}
	return manifest.version;
}

/**
 * @param {string} dir
 * @param {string} base
 * @param {string | undefined} keyOption
 */
function packageChrome(dir, base, keyOption) {
	const zipPath = join(DIST, `${base}.zip`);
	const crxPath = join(DIST, `${base}.crx`);
	const keyPath = keyOption === undefined ? join(DIST, `${base}.pem`) : resolve(ROOT, keyOption);
	const newKey = !existsSync(keyPath);
	rmSync(zipPath, { force: true });
	rmSync(crxPath, { force: true });
	// crx3 一次出两个文件:crx 是签名后的单文件包,zip 是同一份文件集的裸归档
	// (Web Store 上传与「加载已解压的扩展」都用它)。
	run(process.execPath, [CRX3_BIN, '-p', keyPath, '-o', crxPath, '-z', zipPath, dir]);
	const id = extensionId(keyPath);
	if (newKey) {
		console.warn(
			`警告: 新建签名私钥 ${relative(ROOT, keyPath)}(扩展 ID ${id})。每次新建都换 ID,\n` +
				'      已有的安装不会被视为同一个扩展;要固定 ID 请把私钥存进 secret 并用 --key 指过来。'
		);
	} else {
		console.log(`扩展 ID: ${id}`);
	}
	return [zipPath, crxPath];
}

/**
 * @param {string} dir
 * @param {string} base
 * @param {boolean} sign
 */
function packageFirefox(dir, base, sign) {
	const apiKey = process.env.AMO_API_KEY;
	const apiSecret = process.env.AMO_API_SECRET;
	if (sign && (!apiKey || !apiSecret)) {
		fail(
			'--sign 需要 AMO_API_KEY / AMO_API_SECRET 环境变量(addons.mozilla.org 的 JWT issuer / secret)'
		);
	}
	const zipPath = join(DIST, `${base}.zip`);
	const xpiPath = join(DIST, `${base}.xpi`);
	rmSync(zipPath, { force: true });
	rmSync(xpiPath, { force: true });
	// AMO 上传认 zip、安装认 xpi;两者内容一致,先出 zip 再派生 xpi。
	run(process.execPath, [
		WEBEXT_BIN,
		'build',
		'-s',
		dir,
		'-a',
		DIST,
		'--no-config-discovery',
		'--overwrite-dest',
		'--filename',
		`${base}.zip`
	]);

	if (!sign) {
		copyFileSync(zipPath, xpiPath);
		console.warn(
			'警告: xpi 未签名,release 版 Firefox 会拒绝安装(Developer Edition / Nightly 的\n' +
				'      xpinstall.signatures.required=false 才放行,或走 about:debugging 临时加载)。\n' +
				'      配置 AMO_API_KEY / AMO_API_SECRET 后由 CI 在 v* tag 上提交 AMO 签名。'
		);
		return [zipPath, xpiPath];
	}

	// AMO 回传的签名 xpi 用的是 AMO 自己的文件名,所以先签进临时目录再搬过来。
	const stage = join(tmpdir(), `lgom-extension-sign-${process.pid}`);
	rmSync(stage, { recursive: true, force: true });
	const signArgs = [
		WEBEXT_BIN,
		'sign',
		'-s',
		dir,
		'-a',
		stage,
		'--no-config-discovery',
		'--no-input',
		'--channel',
		'unlisted',
		`--api-key=${apiKey}`,
		`--api-secret=${apiSecret}`
	];
	run(process.execPath, signArgs);
	const signed = readdirSync(stage).filter((file) => file.endsWith('.xpi'));
	if (signed.length !== 1) fail(`AMO 签名产物异常: ${signed.join(', ') || '(空)'}`);
	renameSync(join(stage, signed[0]), xpiPath);
	rmSync(stage, { recursive: true, force: true });
	return [zipPath, xpiPath];
}

/**
 * 扩展 ID = 公钥 SPKI DER 的 SHA-256 前 16 字节,每个 nibble 映射到 a-p。
 * @param {string} keyPath
 */
function extensionId(keyPath) {
	const spki = createPublicKey(readFileSync(keyPath, 'utf8')).export({
		type: 'spki',
		format: 'der'
	});
	const digest = createHash('sha256').update(spki).digest().subarray(0, 16);
	const letters = [...digest].map((byte) => `${nibble(byte >> 4)}${nibble(byte & 15)}`);
	return letters.join('');
}

/**
 * @param {number} value
 */
function nibble(value) {
	return String.fromCharCode(97 + value);
}

/**
 * @param {string} file
 * @param {string[]} args
 */
function run(file, args) {
	const command = [file === process.execPath ? '' : file, ...args.map(redact)].join(' ');
	try {
		execFileSync(file, args, { cwd: ROOT, stdio: 'inherit' });
	} catch {
		// 子进程自己的报错已经打到终端了,这里只补一行退出原因,不甩 Node 栈。
		fail(`命令失败: ${command.trim()}`);
	}
}

/**
 * AMO 的 key/secret 是命令行参数,出错时别把它们打进日志(CI 里只有 GitHub 的
 * 掩码兜底,不值得指望)。
 * @param {string} arg
 */
function redact(arg) {
	return arg.startsWith('--api-key=') || arg.startsWith('--api-secret=')
		? `${arg.slice(0, arg.indexOf('='))}=***`
		: arg;
}

/**
 * @param {string} message
 */
function fail(message) {
	console.error(message);
	process.exit(1);
}
