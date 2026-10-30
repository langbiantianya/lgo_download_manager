// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestDocsPageReferencesLocalAssets 验证文档页不引外链资源：静态资源走
// 绝对路径 /docs/...，规范本体走同源 /openapi.json。
func TestDocsPageReferencesLocalAssets(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	page := rec.Body.String()
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Fatalf("文档页里有外链资源:\n%s", page)
	}
	for _, want := range []string{"/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js", "/openapi.json"} {
		if !strings.Contains(page, want) {
			t.Fatalf("文档页里没有引用 %q:\n%s", want, page)
		}
	}
	// 相对路径引资源会在 /docs（无尾斜杠）下解析到 / 下面去，必须避免。
	if strings.Contains(page, `"./swagger-ui`) || strings.Contains(page, `'/swagger-ui`) {
		t.Fatalf("文档页用了相对路径引资源:\n%s", page)
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
		return schemaShape{kind: "integer"}
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
