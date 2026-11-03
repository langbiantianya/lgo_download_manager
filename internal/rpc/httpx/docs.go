// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"

	swaggerFiles "github.com/swaggo/files"
	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

// openAPIJSON 缓存规范转成的 JSON 形式。转换只做一次：规范是嵌入的常量，
// 结果不会变，重复解析纯属浪费。
//
// 失败也会被缓存——这没有问题：输入是编译期就定下来的常量，同一份输入
// 转换的结果永远相同，转换成功与转换失败都是确定的。缓存错误与缓存成功
// 在这里等价，不存在「第一次失败、后来又好了」的情形。
var (
	openAPIJSONOnce sync.Once
	openAPIJSONData []byte
	openAPIJSONErr  error
)

// openAPISpec 返回嵌入的 OpenAPI 规范原文（YAML）。
func openAPISpec() []byte { return openapiYAML }

// openAPIJSON 返回规范的 JSON 形式。转换失败时返回错误，调用方据此回 500。
func openAPIJSON() ([]byte, error) {
	openAPIJSONOnce.Do(func() {
		openAPIJSONData, openAPIJSONErr = yamlToJSON(openapiYAML)
	})
	return openAPIJSONData, openAPIJSONErr
}

// yamlToJSON 把 YAML 文本转成 JSON 字节。转换失败时返回错误而不是 panic：
// 规范是嵌入的常量，正常不会失败，但一旦失败也不该把请求打崩。
func yamlToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// docsPrefix 是文档页静态资源的前缀。
//
// 它是**写死**的常量，不看请求里的任何东西。这一点很关键：先前用
// httpSwagger.WrapHandler 提供的静态资源，它是包级单例，内部用 sync.Once
// 把**进程级**的 swaggerFiles.Handler.Prefix 锁成「首个到达请求」解析出的
// 前缀。http-swagger 的路径正则 ^(.*/)([^?].*)?[?|.]*$ 在「目录路径 + 查询串」
// 上会把前缀回退成 "/"，于是一次 GET /docs/?x=1 就能让本进程内
// /docs/swagger-ui.css 之类的资源**永久** 404。
//
// 现在前缀固定、白名单固定，谁先打都不影响。
const docsPrefix = "/docs/"

// docsAsset 描述一个允许从 /docs/ 提供的静态资源。
type docsAsset struct {
	// name 是资源名：请求路径去掉 docsPrefix 之后剩下的部分。
	name string
	// contentType 是响应的 Content-Type。
	contentType string
}

// docsAssets 是文档页静态资源的白名单：只有这里列出的资源能从 /docs/ 拿到，
// 其余一律 404。
//
// 清单只收本页自己真正引用的资源。swagger-ui 发行版自带的 index.html 与
// swagger-initializer.js 不在其中——它们指向公网的 petstore.swagger.io，
// 本机工具的文档页不该依赖公网，也不该把上游的首页当成本项目的首页。
var docsAssets = []docsAsset{
	{name: "swagger-ui.css", contentType: "text/css; charset=utf-8"},
	{name: "swagger-ui-bundle.js", contentType: "text/javascript; charset=utf-8"},
	{name: "swagger-ui-standalone-preset.js", contentType: "text/javascript; charset=utf-8"},
	{name: "favicon-32x32.png", contentType: "image/png"},
}

// registerDocs 注册文档端点。它们不参与 token 校验——只暴露接口形状，
// 不暴露任务数据。文档端点只接受 GET，其他方法回 405 并带上 Allow 头。
func registerDocs(mux *http.ServeMux) {
	mux.Handle("/openapi.yaml", getOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec())
	})))
	mux.Handle("/openapi.json", getOnly(serveOpenAPIJSON(openAPIJSON)))
	// /docs 自己渲染页面：静态资源用绝对路径引，避免 /docs 被当成「文件」
	// 时相对路径解析到 / 下面去。资源本体由 /docs/ 提供。
	mux.Handle("/docs", getOnly(http.HandlerFunc(writeSwaggerPage)))
	// /docs/ 既是静态资源的挂载点，也是页面本身的第二个入口（/docs/ 与
	// /docs 返回同一个页面，避免尾斜杠差异带来 301 契约漂移）。
	mux.Handle(docsPrefix, getOnly(http.HandlerFunc(serveDocsSubtree)))
}

// serveDocsSubtree 处理 /docs/ 下的请求：
//
//   - 路径正好是 /docs/ → 返回本页自己的页面（与 /docs 相同）；
//   - 路径命中白名单 → 返回对应的静态资源；
//   - 其余 → 404。
//
// 前缀取自写死的 docsPrefix，与「谁先到」无关。
func serveDocsSubtree(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, docsPrefix)
	if !ok {
		writeError(w, http.StatusNotFound, "未知路径 "+r.URL.Path)
		return
	}
	if rest == "" {
		writeSwaggerPage(w, r)
		return
	}
	for _, a := range docsAssets {
		if a.name == rest {
			serveDocsAsset(w, r, a)
			return
		}
	}
	writeError(w, http.StatusNotFound, "没有这个文档资源 "+rest)
}

// serveDocsAsset 把白名单里的静态资源吐出来。资源本体在 swaggo/files 的
// 内存文件系统里（编译进二进制，不依赖公网）。用 http.ServeContent 而不是
// 直接 Write，是为了顺带带上 Content-Length、Last-Modified 与 Range 支持。
func serveDocsAsset(w http.ResponseWriter, r *http.Request, a docsAsset) {
	f, err := swaggerFiles.FS.OpenFile(swaggerFiles.CTX, "/"+a.name, os.O_RDONLY, 0o644)
	if err != nil {
		// 资源读不出来是打包时出的问题，不是调用方的错。
		writeError(w, http.StatusInternalServerError, "读不出文档资源 "+a.name)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读不出文档资源 "+a.name)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	http.ServeContent(w, r, a.name, info.ModTime(), f)
}

// writeSwaggerPage 写出离线的 Swagger UI 页面。
func writeSwaggerPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerPage))
}

// serveOpenAPIJSON 把规范以 JSON 形式吐出来。取 JSON 的方式由参数传入，
// 方便测试注入「转换失败」的情形。
func serveOpenAPIJSON(load func() ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := load()
		if err != nil {
			http.Error(w, "openapi: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
}

// getOnly 把 handler 限制在 GET 上：其他方法回 405 并带上 Allow: GET。
// 错误体沿用全站统一的 {"ok":false,"error":"..."} 形状。
func getOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "只接受 GET")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// swaggerPage 是离线的 Swagger UI 页面：静态资源从同源的 /docs/ 取，
// 规范本体从同源的 /openapi.json 取。整页不引用任何外链资源——本机工具的
// 文档页不该依赖公网。
//
// 资源用绝对路径（/docs/swagger-ui.css）而不是相对路径（./swagger-ui.css）：
// 页面挂在 /docs（无尾斜杠）时，浏览器会把 /docs 当成「文件」，其父目录是
// /，相对路径会解析到 /swagger-ui.css —— 404。
//
// 页面引用的资源必须与 docsAssets 白名单一一对应：openapi_test.go 里的
// TestDocsPageReferencesLocalAssets 与 TestDocsAssetsMatchPageReferences
// 守着这条不变量。
const swaggerPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LGDM Download Service — API 文档</title>
<link rel="stylesheet" type="text/css" href="/docs/swagger-ui.css">
<link rel="icon" type="image/png" href="/docs/favicon-32x32.png" sizes="32x32">
</head>
<body>
<div id="swagger-ui"></div>
<script src="/docs/swagger-ui-bundle.js"></script>
<script src="/docs/swagger-ui-standalone-preset.js"></script>
<script>
window.onload = function () {
  if (typeof SwaggerUIBundle === 'undefined') {
    document.getElementById('swagger-ui').innerHTML =
      '<p>无法加载 Swagger UI 资源。请直接查看 ' +
      '<a href="/openapi.yaml">/openapi.yaml</a> 或 ' +
      '<a href="/openapi.json">/openapi.json</a>。</p>';
    return;
  }
  window.ui = SwaggerUIBundle({
    url: '/openapi.json',
    deepLinking: true,
    docExpansion: 'list',
    dom_id: '#swagger-ui',
    persistAuthorization: true,
    validatorUrl: null,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: 'StandaloneLayout'
  });
};
</script>
</body>
</html>`
