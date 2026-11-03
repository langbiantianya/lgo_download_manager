// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"lgo_download_manager/internal/api"
)

// dtoSchemas 列出「在规范里有 schema 的 DTO」与 schema 名的对应关系。
// 规范里每个 schema 都必须在这里出现，DTO 加字段时同步改规范就会被
// TestOpenAPISchemasMatchDTO 逮住。
var dtoSchemas = []struct {
	schema string
	dto    any
}{
	{"Task", api.Task{}},
	{"Settings", api.Settings{}},
	{"AuthOptions", api.AuthOptions{}},
	{"AddTaskInput", api.AddTaskInput{}},
	{"Event", api.Event{}},
	{"Message", api.Message{}},
	{"ShowAddTaskParams", api.ShowAddTaskParams{}},
	{"BTDetail", api.BTDetail{}},
	{"FileProgress", api.FileProgress{}},
	{"ListParams", api.ListParams{}},
	{"ListResult", api.ListResult{}},
	{"TaskRef", api.TaskRef{}},
	{"BoolResult", api.BoolResult{}},
	{"ProbeParams", api.ProbeParams{}},
	{"ProbeResult", api.ProbeResult{}},
	{"BTInfoParams", api.BTInfoParams{}},
	{"Empty", api.Empty{}},
}

// TestOpenAPIParses 验证规范能转成 JSON，且版本号是 3.0.3。
func TestOpenAPIParses(t *testing.T) {
	doc := parseSpec(t)
	if got := doc["openapi"]; got != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", got)
	}
}

// TestOpenAPICoversAllMethods 双向核对方法与路径：api.AllMethods 里的每个
// 方法都要有路径，规范里也不能有多余的业务路径。
func TestOpenAPICoversAllMethods(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)

	for _, m := range api.AllMethods {
		p := servicePrefix + m
		if _, ok := paths[p]; !ok {
			t.Fatalf("规范缺少方法 %q 的路径 %q", m, p)
		}
	}
	for p := range paths {
		if !strings.HasPrefix(p, servicePrefix) {
			t.Fatalf("规范里有非业务路径 %q", p)
		}
		m := strings.TrimPrefix(p, servicePrefix)
		found := false
		for _, known := range api.AllMethods {
			if known == m {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("规范里有多余的业务路径 %q（api.AllMethods 里没有 %q）", p, m)
		}
	}
}

// TestOpenAPIEndpointsDeclareErrorResponses 验证每个业务端点都声明了实现里
// 真实存在的状态码：200 / 400 / 401 / 405 / 500。
func TestOpenAPIEndpointsDeclareErrorResponses(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	want := []string{"200", "400", "401", "405", "500"}
	for _, m := range api.AllMethods {
		p := servicePrefix + m
		item, ok := paths[p].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 的定义不是对象", p)
		}
		post, ok := item["post"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 post 定义", p)
		}
		responses, ok := post["responses"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 responses 定义", p)
		}
		for _, code := range want {
			if _, ok := responses[code]; !ok {
				t.Fatalf("路径 %q 的响应里缺少 %s", p, code)
			}
		}
	}
}

// TestOpenAPIPathSchemasMatchAPISpec 核对「路径 → schema」的映射：每个方法
// 的请求体与 200 响应必须指向 api.Spec 里声明的那个 DTO。
//
// 只核对路径集合是不够的——路径挂错 schema（把 list 的请求体写成 TaskRef、
// 把 add 的响应写成 BoolResult）一样没人管。这里把两头对上。
func TestOpenAPIPathSchemasMatchAPISpec(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	for _, m := range api.AllMethods {
		spec := api.Spec(m)
		if api.IsStreamMethod(m) {
			// 流式方法没有普通 RPC 规格（api.Spec 对它返回 nil）——
			// 它的请求体是空的、响应是 NDJSON 的消息行，单独断言。
			if spec != nil {
				t.Fatalf("流式方法 %q 不该有普通 RPC 规格", m)
			}
			assertStreamPathSchemas(t, paths, m)
			continue
		}
		if spec == nil {
			t.Fatalf("api.Spec(%q) 是 nil", m)
		}
		p := servicePrefix + m
		item, ok := paths[p].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 的定义不是对象", p)
		}
		post, ok := item["post"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 post 定义", p)
		}

		// 请求体 → api.Spec(method).NewIn()
		rb, ok := post["requestBody"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 requestBody 定义", p)
		}
		wantIn := dtoTypeName(spec.NewIn())
		gotIn := contentSchemaNames(t, rb["content"])
		if !sameSet([]string{wantIn}, gotIn) {
			t.Fatalf("路径 %q 的请求体 schema 不对：规范里是 %v，api.Spec 说是 %q", p, gotIn, wantIn)
		}

		// 200 响应 → api.Spec(method).NewOut()
		responses, ok := post["responses"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 responses 定义", p)
		}
		ok200, ok := responses["200"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 200 响应定义", p)
		}
		wantOut := dtoTypeName(spec.NewOut())
		if api.IsStreamMethod(m) {
			// 流式端点的响应是 NDJSON 的消息行（api.Message），不是
			// api.Spec 里的 NewOut——流式方法压根没有普通 RPC 规格
			// （api.Spec 对它返回 nil）。显式特判。
			wantOut = "Message"
		}
		gotOut := contentSchemaNames(t, ok200["content"])
		if !sameSet([]string{wantOut}, gotOut) {
			t.Fatalf("路径 %q 的 200 响应 schema 不对：规范里是 %v，应当是 %q", p, gotOut, wantOut)
		}
	}
}

// assertStreamPathSchemas 断言流式端点的规范形状。
//
// 流式方法没有普通 RPC 规格：请求体是空的（没有 requestBody 定义），
// 响应是 NDJSON 的消息行（schema 指向 Message 而不是某个请求/响应对）。
func assertStreamPathSchemas(t *testing.T, paths map[string]any, method string) {
	t.Helper()
	p := servicePrefix + method
	item, ok := paths[p].(map[string]any)
	if !ok {
		t.Fatalf("路径 %q 的定义不是对象", p)
	}
	post, ok := item["post"].(map[string]any)
	if !ok {
		t.Fatalf("路径 %q 缺少 post 定义", p)
	}
	// 请求体是空的（Empty）——服务端不读它，但规范里如实声明了形状。
	rb, ok := post["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("流式端点 %q 缺少 requestBody 定义", p)
	}
	gotIn := contentSchemaNames(t, rb["content"])
	if !sameSet([]string{"Empty"}, gotIn) {
		t.Fatalf("路径 %q 的请求体 schema 不对：规范里是 %v，应当是 [Empty]", p, gotIn)
	}
	responses, ok := post["responses"].(map[string]any)
	if !ok {
		t.Fatalf("路径 %q 缺少 responses 定义", p)
	}
	ok200, ok := responses["200"].(map[string]any)
	if !ok {
		t.Fatalf("路径 %q 缺少 200 响应定义", p)
	}
	gotOut := contentSchemaNames(t, ok200["content"])
	if !sameSet([]string{"Message"}, gotOut) {
		t.Fatalf("路径 %q 的 200 响应 schema 不对：规范里是 %v，应当是 [Message]", p, gotOut)
	}
}

// contentSchemaNames 收集 content 下各媒体类型的 schema 名（$ref 的末段）。
func contentSchemaNames(t *testing.T, content any) []string {
	t.Helper()
	m, ok := content.(map[string]any)
	if !ok {
		t.Fatalf("content 不是对象: %#v", content)
	}
	out := make([]string, 0, len(m))
	for _, v := range m {
		cm, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("content 的成员不是对象: %#v", v)
		}
		sm, ok := cm["schema"].(map[string]any)
		if !ok {
			t.Fatalf("content 的成员没有 schema: %#v", cm)
		}
		ref, ok := sm["$ref"].(string)
		if !ok {
			t.Fatalf("schema 不是 $ref: %#v", sm)
		}
		out = append(out, ref[strings.LastIndex(ref, "/")+1:])
	}
	sort.Strings(out)
	return out
}

// dtoTypeName 返回 DTO 指针的类型名。
func dtoTypeName(v any) string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	return t.Name()
}

// TestOpenAPIRequestBodiesAreOptional 验证规范没有把请求体标成必填：
// 服务端容忍空请求体（EOF 不算错），get_settings / stream_messages 的
// 入参本来就是 api.Empty。
func TestOpenAPIRequestBodiesAreOptional(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	for _, m := range api.AllMethods {
		p := servicePrefix + m
		item, _ := paths[p].(map[string]any)
		post, _ := item["post"].(map[string]any)
		rb, ok := post["requestBody"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 requestBody 定义", p)
		}
		raw, ok := rb["required"]
		if !ok {
			continue // 缺省即 false
		}
		if b, ok := raw.(bool); !ok || b {
			t.Fatalf("路径 %q 的 requestBody 不该是必填（规范里是 %v）", p, raw)
		}
	}
}

// TestOpenAPIMessageKindEnumMatchesAPI 验证规范里 Message.kind 的 enum
// 集合与 internal/api 的 MessageKind* 常量集合完全相同。
//
// 常量没法用反射枚举，这里直接解析 internal/api/message.go 的源码——
// 这样 api 那边新增或改掉一个 MessageKind 常量时，这条测试会红。
func TestOpenAPIMessageKindEnumMatchesAPI(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	msg, ok := schemas["Message"].(map[string]any)
	if !ok {
		t.Fatal("规范缺少 schema \"Message\"")
	}
	props, ok := msg["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema \"Message\" 没有 properties")
	}
	kind, ok := props["kind"].(map[string]any)
	if !ok {
		t.Fatal("schema \"Message\" 没有 kind 字段")
	}
	rawEnum, ok := kind["enum"].([]any)
	if !ok {
		t.Fatal("schema \"Message\" 的 kind 没有 enum")
	}
	got := make([]string, 0, len(rawEnum))
	for _, v := range rawEnum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum 的成员不是字符串: %#v", v)
		}
		got = append(got, s)
	}
	sort.Strings(got)

	want := apiMessageKindConsts(t)
	if len(want) == 0 {
		t.Fatal("没从 internal/api/message.go 里解析出 MessageKind 常量")
	}
	if !sameSet(want, got) {
		t.Fatalf("Message.kind 的 enum 与 api 的 MessageKind 常量不一致:\n规范 = %v\napi  = %v", got, want)
	}
}

// apiMessageKindConsts 解析 internal/api/message.go，取出全部 MessageKind*
// 常量的取值。
func apiMessageKindConsts(t *testing.T) []string {
	t.Helper()
	const src = "../../api/message.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", src, err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "MessageKind") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("常量 %s 的值不是字符串字面量", name.Name)
				}
				out = append(out, strings.Trim(lit.Value, `"`))
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestOpenAPISchemasMatchDTO 双向核对 schema 的属性与 DTO 的 JSON 字段。
func TestOpenAPISchemasMatchDTO(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	for _, c := range dtoSchemas {
		s, ok := schemas[c.schema].(map[string]any)
		if !ok {
			t.Fatalf("规范缺少 schema %q", c.schema)
		}
		props, ok := s["properties"].(map[string]any)
		if !ok {
			if len(jsonFields(reflect.TypeOf(c.dto))) != 0 {
				t.Fatalf("schema %q 没有 properties，但 DTO 有字段", c.schema)
			}
			continue
		}
		got := make([]string, 0, len(props))
		for k := range props {
			got = append(got, k)
		}
		want := jsonFields(reflect.TypeOf(c.dto))
		if !sameSet(want, got) {
			t.Fatalf("schema %q 的字段与 DTO 不一致:\nDTO  = %v\n规范 = %v", c.schema, want, got)
		}
	}
}

// TestOpenAPISchemasCoverAllDTOs 验证规范里的 schema 没有漏网的 DTO：
// 规范里每个 schema 都必须在 dtoSchemas 里登记过。
func TestOpenAPISchemasCoverAllDTOs(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	known := map[string]bool{}
	for _, c := range dtoSchemas {
		known[c.schema] = true
	}
	// Error 是错误信封，不是 DTO：它不对应任何请求/响应结构体。
	known["Error"] = true
	for name := range schemas {
		if !known[name] {
			t.Fatalf("规范里的 schema %q 没有对应的 DTO 登记", name)
		}
	}
}

// TestOpenAPISchemaTypesMatchDTO 逐字段核对 OpenAPI 的 type 与 Go 类型。
// 只比字段名不够：字段名对但类型写错（比如把 int64 写成 string）一样会坑人。
func TestOpenAPISchemaTypesMatchDTO(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	for _, c := range dtoSchemas {
		s, ok := schemas[c.schema].(map[string]any)
		if !ok {
			t.Fatalf("规范缺少 schema %q", c.schema)
		}
		props, _ := s["properties"].(map[string]any)
		typ := reflect.TypeOf(c.dto)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := jsonFieldName(f)
			if name == "" {
				continue
			}
			prop, ok := props[name].(map[string]any)
			if !ok {
				t.Fatalf("schema %q 缺少字段 %q", c.schema, name)
			}
			got := parseShape(prop)
			want := expectShape(f.Type)
			if want.ref == "" && want.kind == "" {
				// 映射不了的类型（map、interface 等）不比。
				continue
			}
			checkShape(t, c.schema+"."+name, want, got)
		}
	}
}

// TestOpenAPIReferencesResolve 验证规范里所有 $ref 都指得到东西。
func TestOpenAPIReferencesResolve(t *testing.T) {
	doc := parseSpec(t)
	refs := map[string]bool{}
	collectRefs(doc, refs)
	for ref := range refs {
		parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		if len(parts) != 3 || parts[0] != "components" {
			t.Fatalf("引用 %q 的形式不对", ref)
		}
		var cur any = doc
		for _, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("引用 %q 指向的不是对象", ref)
			}
			cur, ok = m[p]
			if !ok {
				t.Fatalf("引用 %q 指向 %q，但那里没有东西", ref, strings.Join(parts, "."))
			}
		}
	}
}

// TestOpenAPIJSONIsValid 验证 /openapi.json 返回的是合法 JSON。
func TestOpenAPIJSONIsValid(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("不是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", doc["openapi"])
	}
}

// TestOpenAPIYAMLMatchesJSON 验证 /openapi.yaml 与 /openapi.json 是同一份规范。
func TestOpenAPIYAMLMatchesJSON(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/openapi.yaml")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
	if rec.Body.String() != string(openAPISpec()) {
		t.Fatal("/openapi.yaml 的内容与嵌入的规范原文不一致")
	}
	var fromYAML map[string]any
	if err := yaml.Unmarshal(rec.Body.Bytes(), &fromYAML); err != nil {
		t.Fatalf("/openapi.yaml 不是合法 YAML: %v", err)
	}
	// 归一化：YAML 与 JSON 的数字类型不同，两边都过一遍 JSON 再比。
	normalized, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err := json.Unmarshal(normalized, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustJSON(t), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("/openapi.yaml 与 /openapi.json 描述的不是同一份规范")
	}
}

// TestDocsEndpointsNeedNoToken 验证文档端点不参与 token 校验。
func TestDocsEndpointsNeedNoToken(t *testing.T) {
	mux := docsMux()
	cases := []struct {
		path        string
		contentType string
	}{
		{"/openapi.yaml", "application/yaml"},
		{"/openapi.json", "application/json"},
		{"/docs", "text/html"},
		{"/docs/swagger-ui.css", "text/css"},
		{"/docs/swagger-ui-bundle.js", "javascript"},
	}
	for _, c := range cases {
		rec := doGet(mux, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应当 200（不带 token）, got %d", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.contentType) {
			t.Fatalf("%s 的 Content-Type 不对: %q（期望含 %q）", c.path, ct, c.contentType)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 的响应体是空的", c.path)
		}
	}
}

// TestDocsEndpointsRejectNonGet 验证文档端点只接受 GET。
func TestDocsEndpointsRejectNonGet(t *testing.T) {
	mux := docsMux()
	for _, path := range []string{"/openapi.yaml", "/openapi.json", "/docs", "/docs/swagger-ui.css"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s 应当 405, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Fatalf("POST %s 的 Allow 头不对: %q", path, got)
		}
	}
}

// pageTagRe 匹配文档页里**可能发起网络请求**的标签。
//
// 只看这些标签：它们身上的资源属性一旦指向外网，浏览器就会替文档页发起
// 请求，「离线可用」就破了。只抽 <script> / <link> 是不够的——
// <img src>、<iframe src>、<video poster>、<form action> 一样会发请求。
var pageTagRe = regexp.MustCompile(`(?is)<(?:script|link|img|iframe|video|audio|source|embed|object|form|input|track)\b[^>]*>`)

// pageAttrRe 匹配标签里**可能发起网络请求的属性值**。
//
// 属性名覆盖 src、href、data、action、poster、srcset、formaction：
// src/href 是标签的资源地址，data 是 <object> 的资源地址，action 与
// formaction 是表单的提交地址，poster 是视频封面，srcset 是响应式图片
// 的候选列表。
//
// 属性值三种写法都认：双引号、单引号、不带引号（HTML5 允许不带引号，
// 只要求值里不含空白与 >）。只认带引号的写法会漏掉
// `href=//cdn.example.com/evil.css` 这种协议相对 URL。
var pageAttrRe = regexp.MustCompile(`(?is)\b(srcset|formaction|src|href|data|action|poster)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

// pageSpecURLRe 抽出 SwaggerUIBundle 配置里的 url 选项：规范本体的地址。
// 它不是 HTML 属性，但同样是页面引用的资源，必须一起守住。
var pageSpecURLRe = regexp.MustCompile(`(?m)\burl\s*:\s*(?:"([^"]*)"|'([^']*)')`)

// htmlCommentRe 匹配 HTML 注释。
var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// pageProtoRelativeRe 匹配协议相对 URL（//host/path）。
//
// 引号、括号、等号、反引号之后出现 //，都当成外链嫌疑：属性抽取只覆盖
// 白名单里的标签与属性，这条兜底负责在将来有人加了新标签时也能拦住。
//
// // 之后必须是像主机名的字符：协议相对 URL 的主机名不会以空白或非 ASCII
// 开头，这样普通的中文 JS 注释（//注释）不会被误判。
var pageProtoRelativeRe = regexp.MustCompile("[\"'(`=]\\s*//[A-Za-z0-9._-]")

// pageLocalPrefixes 是页面里允许引用的本地路径前缀。
var pageLocalPrefixes = []string{"/docs/", "/openapi.json", "/openapi.yaml"}

// TestDocsPageReferencesLocalAssets 验证文档页不引外链资源：静态资源走
// 同源绝对路径 /docs/...，规范本体走同源 /openapi.json。
//
// 判定只看属性值，不做子串匹配；协议相对 URL（//cdn...）、大写 scheme、
// data: URI、相对路径都必须被逮住。抽取覆盖所有可能发起网络请求的属性
// （src / href / data / action / poster / srcset / formaction）与标签
// （script / link / img / iframe / video / audio / source / embed /
// object / form / input / track），属性值带不带引号都抽得到。
func TestDocsPageReferencesLocalAssets(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	page := rec.Body.String()

	// 三道判定合成一处：属性值逐个检查、http(s) 兜底、协议相对 URL 兜底。
	if err := pageViolation(page); err != nil {
		t.Fatalf("文档页里有外链资源: %v\n%s", err, page)
	}

	refs := pageRefs(page)
	if len(refs) == 0 {
		t.Fatalf("文档页里没解析出任何资源引用:\n%s", page)
	}

	// 正向断言：页面引用的资源集合必须**恰好**是期望的那些。
	// 少一个说明功能坏了，多一个说明有人塞进了没登记的资源。
	want := []string{
		"/docs/swagger-ui.css",
		"/docs/swagger-ui-bundle.js",
		"/docs/swagger-ui-standalone-preset.js",
		"/docs/favicon-32x32.png",
		"/openapi.json",
	}
	got := append([]string(nil), refs...)
	sort.Strings(got)
	if !sameSet(want, got) {
		t.Fatalf("文档页引用的资源不对:\n实际 = %v\n期望 = %v", got, want)
	}
}

// pageRefs 抽出页面引用的全部资源地址：标签里可能发起网络请求的属性值，
// 外加 SwaggerUIBundle 配置里的规范地址。
//
// 只认属性值，不做整页子串匹配：一行 HTML 注释不该能满足断言。匹配时
// 跳过 HTML 注释（注释里的内容不算页面引用）。
func pageRefs(page string) []string {
	stripped := htmlCommentRe.ReplaceAllString(page, "")
	var out []string
	for _, tag := range pageTagRe.FindAllString(stripped, -1) {
		for _, m := range pageAttrRe.FindAllStringSubmatch(tag, -1) {
			value := firstNonEmpty(m[2], m[3], m[4])
			if m[1] == "srcset" {
				// srcset 的值是逗号分隔的候选列表，逐个候选单独判定。
				out = append(out, srcsetURLs(value)...)
				continue
			}
			out = append(out, value)
		}
	}
	for _, m := range pageSpecURLRe.FindAllStringSubmatch(stripped, -1) {
		out = append(out, firstNonEmpty(m[1], m[2]))
	}
	return out
}

// srcsetURLs 把 srcset 的值拆成候选 URL 列表。
//
// srcset 的语法是逗号分隔的候选列表，每个候选是「URL」或「URL 描述符」
// （描述符是 1x/2x/100w 之类）。必须逐个候选单独判定，否则
// 「/docs/a.png 1x, //cdn/b.png 2x」这种混合写法能从缝里钻过去。
func srcsetURLs(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		out = append(out, fields[0])
	}
	return out
}

// pageViolation 返回页面违反「只用同源本地资源」的第一条原因；没问题返回 nil。
//
// 三道判定都在这儿：标签里的资源属性逐个检查、整页不许出现 http(s) 外链、
// 整页不许出现协议相对 URL（//host/path）。正式测试与护栏自检共用它，
// 免得两处口径漂移。
func pageViolation(page string) error {
	for _, ref := range pageRefs(page) {
		if err := localRefErr(ref); err != nil {
			return err
		}
	}
	// 兜底：整页不该出现任何 http(s) 链接。这一条挡不住协议相对 URL，
	// 但能挡住最常见的 CDN 回潮。
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		return errors.New("页面里有 http(s) 外链")
	}
	// 兜底：整页不该出现协议相对 URL。属性抽取只覆盖白名单里的标签与属性，
	// 将来有人加了新标签时，这条兜底仍然能兜住。
	if loc := pageProtoRelativeRe.FindString(page); loc != "" {
		return fmt.Errorf("页面里有协议相对 URL（%q）", loc)
	}
	return nil
}

// localRefErr 返回一个资源引用违反「只用同源本地资源」时的原因；没问题返回 nil。
//
// 口径：必须以 / 开头（挡相对路径与 data: URI）、必须不以 // 开头（挡协议
// 相对 URL）、必须不含 :（挡 http: / https: / data: / 大写 scheme），
// 最后必须落在允许的本地路径前缀之内。
func localRefErr(ref string) error {
	switch {
	case ref == "":
		return errors.New("页面里有空的资源引用")
	case !strings.HasPrefix(ref, "/"):
		return fmt.Errorf("资源引用 %q 不是绝对路径（相对路径在 /docs 下会解析到 / 下面去）", ref)
	case strings.HasPrefix(ref, "//"):
		return fmt.Errorf("资源引用 %q 是协议相对 URL", ref)
	case strings.Contains(ref, ":"):
		return fmt.Errorf("资源引用 %q 带 scheme（http:/https:/data: 等）", ref)
	}
	for _, p := range pageLocalPrefixes {
		if strings.HasPrefix(ref, p) {
			return nil
		}
	}
	return fmt.Errorf("资源引用 %q 不在允许的本地路径前缀里（允许：%v）", ref, pageLocalPrefixes)
}

// TestDocsPageRejectsExternalReferences 是「防 CDN 回潮」护栏的**自检**。
//
// TestDocsPageReferencesLocalAssets 只跑真实页面，穷举不了各种绕过写法；
// 这里把绕过写法固化成用例：每一种都必须被判成外链。判定走的是与正式
// 测试**同一套**代码（pageViolation），不是另写一份口径。
func TestDocsPageRejectsExternalReferences(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"属性值不加引号", `<link rel="stylesheet" href=//cdn.example.com/evil.css>`},
		{"img 的 src", `<img src="//cdn.example.com/tracker.png">`},
		{"iframe 的 src", `<iframe src="//cdn.example.com/frame.html">`},
		{"video 的 poster", `<video poster="//cdn.example.com/p.jpg">`},
		{"form 的 action", `<form action="//cdn.example.com/post">`},
		{"img 的 srcset", `<img srcset="//cdn.example.com/a.png 1x">`},
		{"object 的 data", `<object data="//cdn.example.com/x.svg">`},
		{"协议相对 URL", `<link rel="stylesheet" href="//cdn.example.com/evil.css">`},
		{"大写 scheme", `<link rel="stylesheet" href="HTTPS://cdn.example.com/evil.css">`},
		{"data: URI", `<link rel="stylesheet" href="data:text/css,body{}">`},
		{"不带 ./ 的相对路径", `<link rel="stylesheet" href="swagger-ui.css">`},
		{"注释里的外链", `<!-- <link rel="stylesheet" href="//cdn.example.com/evil.css"> -->`},
		{"复合场景", `<script src="/docs/swagger-ui-bundle.js"></script>` +
			`<img src="//cdn.example.com/t.png"><link href="HTTPS://x/y.css">`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := pageViolation(c.html); err == nil {
				t.Fatalf("护栏放过了外链（页面片段：%q）", c.html)
			}
		})
	}
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// TestDocsAssetsMatchPageReferences 双向核对白名单与页面引用：页面引用的
// 每个静态资源都要在白名单里，白名单里的每个资源都要被页面引用。
func TestDocsAssetsMatchPageReferences(t *testing.T) {
	page := doGet(docsMux(), "/docs").Body.String()
	referenced := map[string]bool{}
	for _, ref := range pageRefs(page) {
		if !strings.HasPrefix(ref, docsPrefix) {
			continue
		}
		referenced[strings.TrimPrefix(ref, docsPrefix)] = true
	}
	for _, a := range docsAssets {
		if !referenced[a.name] {
			t.Fatalf("白名单里的资源 %q 没有被文档页引用", a.name)
		}
	}
	for name := range referenced {
		found := false
		for _, a := range docsAssets {
			if a.name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("文档页引用了白名单外的资源 %q", name)
		}
	}
}

// TestDocsStaticAssetsHaveFixedPrefix 是静态资源前缀的回归测试。
//
// 先前静态资源由 httpSwagger.WrapHandler 提供，它是包级单例，内部用
// sync.Once 把**进程级**的 swaggerFiles.Handler.Prefix 锁成「首个到达请求」
// 解析出的前缀。一次 GET /docs/?x=1 就能把前缀锁成 "/" ，此后本进程内的
// 静态资源全部 404。现在前缀是写死的，谁先打都不影响。
func TestDocsStaticAssetsHaveFixedPrefix(t *testing.T) {
	mux := docsMux()
	// 先打带查询串的目录请求。
	rec := doGet(mux, "/docs/?x=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs/?x=1 应当 200, got %d", rec.Code)
	}
	// 再打静态资源：必须仍然是 200 且有内容。
	for _, a := range docsAssets {
		rec := doGet(mux, docsPrefix+a.name)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s 应当 200, got %d", docsPrefix+a.name, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("GET %s 的响应体是空的", docsPrefix+a.name)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, a.contentType) {
			t.Fatalf("GET %s 的 Content-Type 不对: %q（期望 %q）", docsPrefix+a.name, ct, a.contentType)
		}
	}
	// 换个顺序再来一遍：资源先到，结果必须一样。
	mux2 := docsMux()
	for _, a := range docsAssets {
		if rec := doGet(mux2, docsPrefix+a.name); rec.Code != http.StatusOK {
			t.Fatalf("GET %s 应当 200, got %d", docsPrefix+a.name, rec.Code)
		}
	}
	if rec := doGet(mux2, "/docs/?x=2"); rec.Code != http.StatusOK {
		t.Fatalf("GET /docs/?x=2 应当 200, got %d", rec.Code)
	}
}

// TestDocsPathContract 核对文档端点的实际行为与端点契约一致。
func TestDocsPathContract(t *testing.T) {
	mux := docsMux()
	cases := []struct {
		path        string
		wantCode    int
		contentType string
	}{
		{"/docs", http.StatusOK, "text/html"},
		{"/docs/", http.StatusOK, "text/html"},
		{"/openapi.yaml", http.StatusOK, "application/yaml"},
		{"/openapi.json", http.StatusOK, "application/json"},
		{"/docs/swagger-ui.css", http.StatusOK, "text/css"},
		{"/docs/swagger-ui-bundle.js", http.StatusOK, "javascript"},
		// 白名单外的路径一律 404。/docs/doc.json 先前由 http-swagger 内建
		// 路径处理，稳定吐 500；/docs/index.html 与 /docs/swagger-initializer.js
		// 是 swagger-ui 自带的资源，不该被端出去。
		{"/docs/doc.json", http.StatusNotFound, ""},
		{"/docs/index.html", http.StatusNotFound, ""},
		{"/docs/swagger-initializer.js", http.StatusNotFound, ""},
		{"/docs/nope.txt", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		rec := doGet(mux, c.path)
		if rec.Code != c.wantCode {
			t.Fatalf("GET %s 应当 %d, got %d", c.path, c.wantCode, rec.Code)
		}
		if c.contentType != "" {
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.contentType) {
				t.Fatalf("GET %s 的 Content-Type 不对: %q（期望含 %q）", c.path, ct, c.contentType)
			}
		}
	}
	// /docs 与 /docs/ 必须是同一个页面：尾斜杠差异不该造成契约漂移。
	if a, b := doGet(mux, "/docs").Body.String(), doGet(mux, "/docs/").Body.String(); a != b {
		t.Fatal("/docs 与 /docs/ 返回的页面不一致")
	}
}

// TestOpenAPIJSONFailsGracefully 验证 YAML→JSON 转换失败时端点回 500，
// 而不是把请求打崩。
func TestOpenAPIJSONFailsGracefully(t *testing.T) {
	// 坏 YAML：转换必须返回错误。
	if _, err := yamlToJSON([]byte("\tbad: [unclosed")); err == nil {
		t.Fatal("坏 YAML 应当转换失败")
	}
	// 端点在转换失败时回 500。
	h := serveOpenAPIJSON(func() ([]byte, error) {
		return nil, errors.New("炸了")
	})
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("转换失败应当 500, got %d", rec.Code)
	}
}

// docsMux 造一个带文档端点的 mux。文档端点不参与鉴权，这里照常给业务端点
// 设 token，确保鉴权没被误挂到文档端点上。
func docsMux() *http.ServeMux {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	return newMux(h, "secret")
}

// doGet 发起一次不带 Authorization 头的 GET 请求。
func doGet(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// parseSpec 解析规范（转成 JSON 之后的形态）。
func parseSpec(t *testing.T) map[string]any {
	t.Helper()
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatalf("规范转 JSON 失败: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("规范不是合法 JSON: %v", err)
	}
	return doc
}

// mustJSON 返回规范的 JSON 形态。
func mustJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatalf("规范转 JSON 失败: %v", err)
	}
	return raw
}

// specPaths 取规范里的 paths。
func specPaths(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 paths")
	}
	return paths
}

// specSchemas 取规范里的 components.schemas。
func specSchemas(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 components.schemas")
	}
	return schemas
}

// jsonFieldName 返回结构体字段的 JSON 名字；没有 json tag 的字段返回空串。
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

// jsonFields 返回结构体的 json tag 字段名集合（排序后）。
func jsonFields(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := jsonFieldName(t.Field(i))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// sameSet 比较两个字符串集合是否相同（多重集语义）。
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// timeType 是 time.Time 的类型，用来把时间戳字段识别成 date-time 字符串。
var timeType = reflect.TypeOf(time.Time{})

// schemaShape 描述一个 OpenAPI schema 节点的类型信息。
type schemaShape struct {
	ref    string       // $ref 指向的 schema 名
	kind   string       // type：string/boolean/integer/number/array/object
	format string       // format
	items  *schemaShape // array 的元素类型
}

// parseShape 从规范的 JSON 形态里读出一个 schema 节点的类型信息。
func parseShape(v any) schemaShape {
	m, ok := v.(map[string]any)
	if !ok {
		return schemaShape{}
	}
	var s schemaShape
	if ref, ok := m["$ref"].(string); ok {
		s.ref = ref[strings.LastIndex(ref, "/")+1:]
		return s
	}
	s.kind, _ = m["type"].(string)
	s.format, _ = m["format"].(string)
	if it, ok := m["items"]; ok {
		child := parseShape(it)
		s.items = &child
	}
	return s
}

// expectShape 按 Go 类型推出应有的 OpenAPI 类型。映射规则：
// string→string、bool→boolean、整数→integer、浮点→number、
// 切片→array、time.Time→string(date-time)、嵌套结构体→$ref。
func expectShape(t reflect.Type) schemaShape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return schemaShape{kind: "string", format: "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return schemaShape{kind: "string"}
	case reflect.Bool:
		return schemaShape{kind: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schemaShape{kind: "integer", format: integerFormat(t)}
	case reflect.Float32, reflect.Float64:
		return schemaShape{kind: "number"}
	case reflect.Slice, reflect.Array:
		item := expectShape(t.Elem())
		return schemaShape{kind: "array", items: &item}
	case reflect.Struct:
		return schemaShape{ref: t.Name()}
	default:
		return schemaShape{}
	}
}

// integerFormat 返回整数类型在 OpenAPI 里应当声明的 format。
//
// int64 必须写 int64、int32 必须写 int32——把 int64 写成 int32 会让生成
// 出来的客户端按 32 位宽度截断。Go 的 int 与更窄的整型宽度不稳定（int
// 随平台变），不强制声明 format。
func integerFormat(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int64:
		return "int64"
	case reflect.Int32:
		return "int32"
	default:
		return ""
	}
}

// checkShape 比对期望类型与规范里的类型。
func checkShape(t *testing.T, field string, want, got schemaShape) {
	t.Helper()
	if want.ref != "" {
		if got.ref != want.ref {
			t.Fatalf("字段 %s 应当 $ref %q，规范里是 %q", field, want.ref, got.ref)
		}
		return
	}
	if got.ref != "" {
		t.Fatalf("字段 %s 应当是 %s，规范里却是 $ref %q", field, want.kind, got.ref)
	}
	if got.kind != want.kind {
		t.Fatalf("字段 %s 的 type 应当是 %q，规范里是 %q", field, want.kind, got.kind)
	}
	if want.format == "date-time" {
		if got.format != "date-time" {
			t.Fatalf("字段 %s 应当声明 format: date-time，规范里是 %q", field, got.format)
		}
	} else if got.format == "date-time" {
		t.Fatalf("字段 %s 不是时间戳，不该声明 format: date-time", field)
	}
	if want.kind == "integer" {
		switch {
		case want.format != "":
			if got.format != want.format {
				t.Fatalf("字段 %s 应当声明 format: %s，规范里是 %q", field, want.format, got.format)
			}
		case got.format != "" && got.format != "int32" && got.format != "int64":
			t.Fatalf("字段 %s 的 format %q 不是合法的整数 format", field, got.format)
		}
	}
	if want.kind == "array" {
		if got.items == nil {
			t.Fatalf("字段 %s 是数组，规范里没有 items", field)
		}
		checkShape(t, field+"[]", *want.items, *got.items)
	}
}

// collectRefs 递归收集文档里所有的 $ref。
func collectRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out[s] = true
				}
				continue
			}
			collectRefs(val, out)
		}
	case []any:
		for _, item := range x {
			collectRefs(item, out)
		}
	}
}

// TestOpenAPIRequiredFieldsMatchContract 守住规范里 required 列表与契约
// DTO 的一致性。
//
// 现有的一致性测试（TestOpenAPISchemaTypesMatchDTO）只查「属性在不在、
// 类型对不对」，**不查 required**。于是规范里把某个必填字段从 required
// 里删掉，测试照样绿——而那意味着文档说「可以不填」，实际代码却因为
// 字段缺失而报错。
//
// 期望值写成**字面量**而不是从 DTO 派生：derived 的写法会跟着实现一起
// 变，改错了也抓不到。
func TestOpenAPIRequiredFieldsMatchContract(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)

	cases := []struct {
		schema string
		want   []string
	}{
		{"TaskRef", []string{"id"}},
		{"ProbeParams", []string{"url"}},
		{"AddTaskInput", []string{"url"}},
		{"BTInfoParams", []string{"task_id"}},
		{"Error", []string{"ok", "error"}},
	}
	for _, c := range cases {
		t.Run(c.schema, func(t *testing.T) {
			s, ok := schemas[c.schema].(map[string]any)
			if !ok {
				t.Fatalf("规范缺少 schema %q", c.schema)
			}
			raw, ok := s["required"].([]any)
			if !ok {
				t.Fatalf("schema %q 没有 required 列表", c.schema)
			}
			got := make([]string, 0, len(raw))
			for _, v := range raw {
				str, ok := v.(string)
				if !ok {
					t.Fatalf("schema %q 的 required 里有非字符串项: %v", c.schema, v)
				}
				got = append(got, str)
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("schema %q 的 required = %v，期望 %v", c.schema, got, want)
			}
		})
	}
}
