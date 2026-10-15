package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
)

// TestDefaultInputsGolden 锁定代表性节点由 XxxInput 默认值推导出的 DefaultInputs。
// 空字符串与 null 不进入 DefaultInputs（如 HTTP 的 URL），因此可以按 JSON 原文精确比较。
func TestDefaultInputsGolden(t *testing.T) {
	want := map[string]string{
		Template: `{"Outputs":[]}`,
		HTTP:     `{"Method":"GET","TimeoutSec":30}`,
		Switch:   `{}`,
		LLM:      `{"Prompt":"{{Query}}"}`,
	}
	for _, n := range append(Nodes(), HTTPNode{}, LLMNode{}) {
		spec := n.Spec()
		expect, ok := want[spec.ID]
		if !ok {
			continue
		}
		obj := map[string]json.RawMessage{}
		for k, ref := range spec.DefaultInputs {
			obj[k] = ref.Value
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("%s: %v", spec.ID, err)
		}
		if string(raw) != expect {
			t.Errorf("%s DefaultInputs:\n got %s\nwant %s", spec.ID, raw, expect)
		}
	}
}

// TestInlineKinds 锁定内置节点的内联 / 活动划分（设计文档 4.4）：
// map、subgraph 因实现 Controller 自动内联；template 显式声明 Inline；reduce、code、http、llm 为活动节点。
func TestInlineKinds(t *testing.T) {
	reg, err := node.NewRegistry(append(Nodes(), HTTPNode{}, LLMNode{})...)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		Start: true, End: true, Switch: true, Merge: true, Parallel: true, StopError: true,
		Template: true, Map: true, SubGraph: true,
		Reduce: false, Code: false, HTTP: false, LLM: false,
	}
	for _, spec := range reg.List() {
		inline, ok := want[spec.ID]
		if !ok {
			t.Errorf("%s: missing from expectation table", spec.ID)
			continue
		}
		if got := reg.Inline(spec.ID); got != inline {
			t.Errorf("%s: Inline = %v, want %v", spec.ID, got, inline)
		}
	}
	if reg.Inline("missing") {
		t.Error("unregistered kind reported inline")
	}
	// Inline 只供后端判定，不随节点库下发。
	raw, err := json.Marshal(reg.List())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"Inline"`) {
		t.Error("Inline must not be serialized")
	}
}

// TestOutputFromHTTP 确认 HTTPOutput 推导出默认出口上的 Body / Status / OK 及其类型。
func TestOutputFromHTTP(t *testing.T) {
	byEndpoint := node.OutputFrom[HTTPOutput]()
	schema := byEndpoint[node.EndpointPayloadDefault]
	if schema == nil {
		t.Fatal("expected default endpoint schema")
	}
	if names := jsonx.PropertyNames(schema); len(names) != 3 || names[0] != "Body" {
		t.Fatalf("names = %v", names)
	}
	if schema.Properties["Body"].Type != "string" || schema.Properties["Status"].Type != "integer" || schema.Properties["OK"].Type != "boolean" {
		t.Fatalf("types Body=%q Status=%q OK=%q", schema.Properties["Body"].Type, schema.Properties["Status"].Type, schema.Properties["OK"].Type)
	}
}

// TestOutputFromMap 确认 MapOutput 只在默认出口上声明 Results。
func TestOutputFromMap(t *testing.T) {
	byEndpoint := node.OutputFrom[MapOutput]()
	if len(byEndpoint) != 1 {
		t.Fatalf("byEndpoint = %#v", byEndpoint)
	}
	if names := jsonx.PropertyNames(byEndpoint[node.EndpointPayloadDefault]); len(names) != 1 || names[0] != MapResultsField {
		t.Fatalf("map output names = %v", names)
	}
}

// TestOutputMapPreservesTypes 确认 OutputMap 把输出结构体转成 map 时保留 int64 与 bool。
func TestOutputMapPreservesTypes(t *testing.T) {
	got := node.OutputMap(HTTPOutput{Body: "x", Status: 200, OK: true})
	if got["Body"] != "x" || got["Status"] != int64(200) || got["OK"] != true {
		t.Fatalf("got %#v", got)
	}
}
