// Node 的模块解析钩子。
//
// 扩展的平台层用 `$lib/xxx` 引用 `src/lib/`（Vite 的别名，只在构建时
// 生效），Node 直接加载源码时得自己把这个别名接上。
//
// 用法（必须在 import 扩展模块之前调用）：
//
//   import { register } from 'node:module';
//   register('./alias-loader.mjs', import.meta.url);

const LIB = new URL('../../src/lib/', import.meta.url);

/**
 * @param {string} specifier
 * @param {unknown} context
 * @param {(s: string, c: unknown) => Promise<any>} nextResolve
 */
export async function resolve(specifier, context, nextResolve) {
	if (specifier.startsWith('$lib/')) {
		return {
			url: new URL(specifier.slice('$lib/'.length), LIB).href,
			shortCircuit: true
		};
	}
	return nextResolve(specifier, context);
}
