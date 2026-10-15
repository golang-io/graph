package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/golang-io/graph"
	"github.com/google/jsonschema-go/jsonschema"
)

// TestParamsDecodeKeepsInt64 确认编译期 decodeParams 把 any 字段中的整数保持为 int64，
// 与引擎运行变量的数字类型一致；小数仍为 float64；运行期 Params[T] 直接读取冻结的结构体实例。
func TestParamsDecodeKeepsInt64(t *testing.T) {
	type cfg struct {
		V any            `json:"V"`
		M map[string]any `json:"M"`
		F any            `json:"F"`
	}
	spec := WithParams(NodeSpec{ID: "p", Name: "p", Endpoints: DefaultEndpoints()}, cfg{})
	decoded, err := spec.decodeParams(map[string]graph.InputRef{
		"V": {Value: json.RawMessage(`3`)},
		"M": {Value: json.RawMessage(`{"x":4}`)},
		"F": {Value: json.RawMessage(`1.5`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Params[cfg](decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.V.(int64); !ok {
		t.Errorf("V = %T, want int64", got.V)
	}
	if _, ok := got.M["x"].(int64); !ok {
		t.Errorf("M.x = %T, want int64", got.M["x"])
	}
	if _, ok := got.F.(float64); !ok {
		t.Errorf("F = %T, want float64", got.F)
	}
}

type countedHookParams struct {
	X string `json:"X"`
}

var paramDecodeCalls int

func (c *countedHookParams) UnmarshalJSON(data []byte) error {
	paramDecodeCalls++
	var raw struct {
		X string `json:"X"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.X = raw.X
	return nil
}

func (countedHookParams) Validate() error                    { return nil }
func (countedHookParams) DeclareOutputs() *jsonschema.Schema { return OutputSchema("out") }
func (countedHookParams) Expressions() []Expression          { return nil }
func (countedHookParams) JoinMode() string                   { return JoinAll }
func (countedHookParams) References() []NamedRef             { return nil }
func (countedHookParams) Subgraphs() []Subgraph              { return nil }
func (countedHookParams) ActiveOutputs() []string            { return []string{DefaultOutput} }

// TestValidateDecodesParamsOncePerNode 确认 Validate / ValidateGraph 对每个节点实例只解码一次静态参数，
// 直接对解码后的结构体做全部钩子接口断言。
func TestValidateDecodesParamsOncePerNode(t *testing.T) {
	entry := stubNode{spec: WithParams(NodeSpec{
		ID: "entry", Name: "入口", Endpoints: EndpointSpec{Outputs: []string{DefaultOutput}}, Inline: true,
	}, struct{}{})}
	work := stubNode{spec: WithParams(NodeSpec{
		ID: "work", Name: "工作", Endpoints: DefaultEndpoints(),
	}, countedHookParams{X: "init"})}
	end := stubNode{spec: WithParams(NodeSpec{
		ID: "end", Name: "结束", Endpoints: EndpointSpec{Inputs: []string{DefaultInput}}, Inline: true,
	}, struct{}{})}
	r, err := NewRegistry(entry, work, end)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "s", Kind: "entry"},
			{ID: "w", Kind: "work", Inputs: map[string]graph.InputRef{"X": {Value: json.RawMessage(`"hello"`)}}},
			{ID: "e", Kind: "end", Inputs: map[string]graph.InputRef{"out": {From: "w", Field: "out"}}},
		},
		Edges: []graph.Edge{
			{ID: "e1", From: "s.Out", To: "w.In"},
			{ID: "e2", From: "w.Out", To: "e.In"},
		},
	}
	paramDecodeCalls = 0
	v, err := r.ValidateGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if paramDecodeCalls != 1 {
		t.Fatalf("decodeParams called %d times for node w, want 1", paramDecodeCalls)
	}
	got, err := Params[countedHookParams](v.Params["w"])
	if err != nil || got.X != "hello" {
		t.Fatalf("frozen params = %#v, err = %v", got, err)
	}
	_ = context.Background()
}

// TestResolveParamsMergesDynamicAndInjectedOverrides 确认运行期 ResolveParams：
//   - 无动态绑定且无调试注入时直接返回编译期静态参数 base；
//   - 存在动态绑定（From != ""）或调试注入覆盖参数结构体字段时，合入运行期值且不修改编译期缓存的 base；
//   - 覆盖值类型不匹配时由 Params[T] 返回错误。
func TestResolveParamsMergesDynamicAndInjectedOverrides(t *testing.T) {
	type sampleCfg struct {
		URL        string            `json:"URL,omitempty"`
		TimeoutSec int               `json:"TimeoutSec,omitempty"`
		Headers    map[string]string `json:"Headers,omitempty"`
		Query      any               `json:"Query,omitempty"`
	}
	nSpec := stubNode{spec: WithParams(NodeSpec{
		ID: "sample", Name: "样例", Endpoints: DefaultEndpoints(),
	}, sampleCfg{})}
	r, err := NewRegistry(nSpec)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 纯字面量参数且无调试注入：直接复用 base。
	staticNode := graph.Node{
		ID: "n1", Kind: "sample",
		Inputs: map[string]graph.InputRef{
			"URL":     {Value: json.RawMessage(`"https://prod.example.com"`)},
			"Headers": {Value: json.RawMessage(`{"X-Env":"prod"}`)},
			"extra":   {From: "$input", Field: "extra"}, // 非参数结构体字段的动态输入不触发合并
		},
	}
	_, base, err := r.decodeNode(staticNode)
	if err != nil {
		t.Fatal(err)
	}
	resolved := r.ResolveParams(staticNode, base, map[string]any{
		"URL":     "https://prod.example.com",
		"Headers": map[string]any{"X-Env": "prod"},
		"extra":   "val",
	}, nil)
	got, err := Params[sampleCfg](resolved)
	if err != nil || got.URL != "https://prod.example.com" || got.Headers["X-Env"] != "prod" {
		t.Fatalf("static resolve = %#v, err = %v", got, err)
	}

	// 2. 调试注入覆盖已有字面量参数字段（URL 与 Headers），且不污染 base。
	inj := map[string]any{
		"URL":     "http://mock.local",
		"Headers": map[string]any{"X-Env": "mock"},
	}
	vars := map[string]any{
		"URL":     "http://mock.local",
		"Headers": map[string]any{"X-Env": "mock"},
	}
	injectedResolved := r.ResolveParams(staticNode, base, vars, inj)
	injGot, err := Params[sampleCfg](injectedResolved)
	if err != nil || injGot.URL != "http://mock.local" || injGot.Headers["X-Env"] != "mock" {
		t.Fatalf("injected resolve = %#v, err = %v", injGot, err)
	}
	baseCfg, _ := Params[sampleCfg](base)
	if baseCfg.URL != "https://prod.example.com" || baseCfg.Headers["X-Env"] != "prod" {
		t.Fatalf("base mutated by ResolveParams: %#v", baseCfg)
	}

	// 3. 动态绑定参数字段（From != ""）及类型错误报告。
	dynNode := graph.Node{
		ID: "n2", Kind: "sample",
		Inputs: map[string]graph.InputRef{
			"URL":        {From: "$input", Field: "url"},
			"TimeoutSec": {From: "$input", Field: "timeout"},
			"Query":      {From: "$input", Field: "q"},
		},
	}
	_, dynBase, err := r.decodeNode(dynNode)
	if err != nil {
		t.Fatal(err)
	}
	dynResolved := r.ResolveParams(dynNode, dynBase, map[string]any{
		"URL":        "https://dyn.example.com",
		"TimeoutSec": int64(15),
		"Query":      map[string]any{"k": int64(1)},
	}, nil)
	dynGot, err := Params[sampleCfg](dynResolved)
	if err != nil || dynGot.URL != "https://dyn.example.com" || dynGot.TimeoutSec != 15 {
		t.Fatalf("dynamic resolve = %#v, err = %v", dynGot, err)
	}

	badResolved := r.ResolveParams(dynNode, dynBase, map[string]any{
		"URL":        []any{"not-a-string"},
		"TimeoutSec": int64(15),
	}, nil)
	if _, err := Params[sampleCfg](badResolved); err == nil {
		t.Fatal("expected error when dynamic parameter has incompatible type")
	}

	// 4. 仅覆盖标量字段（走快路径）时，未覆盖的 map 字段同样与 base 隔离，修改返回值不影响 base。
	scalarOnlyInj := map[string]any{"URL": "http://fast.local"}
	scalarOnlyVars := map[string]any{"URL": "http://fast.local"}
	fastResolved := r.ResolveParams(staticNode, base, scalarOnlyVars, scalarOnlyInj)
	fastGot, err := Params[sampleCfg](fastResolved)
	if err != nil {
		t.Fatal(err)
	}
	fastGot.Headers["X-Env"] = "tampered"
	baseAfterFast, _ := Params[sampleCfg](base)
	if baseAfterFast.Headers["X-Env"] != "prod" {
		t.Fatalf("base map mutated after scalar fast-path override: %#v", baseAfterFast)
	}
}
