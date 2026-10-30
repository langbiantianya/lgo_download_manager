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
	"sync"

	httpSwagger "github.com/swaggo/http-swagger"
	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

// openAPIJSON 缓存规范转成的 JSON 形式。转换只做一次：规范是嵌入的常量，
// 结果不会变，重复解析纯属浪费。
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
	mux.Handle("/docs", getOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerPage))
	})))
	// /docs/ 交给 http-swagger：它把 swagger-ui 静态资源（已嵌进二进制）
	// 从内存文件系统里吐出来，不依赖公网 CDN。
	mux.Handle("/docs/", getOnly(httpSwagger.WrapHandler))
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
