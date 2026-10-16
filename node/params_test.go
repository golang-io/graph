package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/golang-io/graph"
	"github.com/google/jsonschema-go/jsonschema"
)

// TestParamsDecodeKeepsInt64 确认 decodeParams 把 any 字段中的整数保持为 int64，与引擎运行变量一致。
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

// TestValidateDecodesParamsOncePerNode 确认 Validate 对每个节点实例只解码一次静态参数。
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
	if err := r.Validate(g); err != nil {
		t.Fatal(err)
	}
	if paramDecodeCalls != 1 {
		t.Fatalf("decodeParams called %d times for node w, want 1", paramDecodeCalls)
	}
}

// TestResolveParamsMergesDynamicAndInjectedOverrides 确认运行期 ResolveParams 单次合并解码字面量与动态绑定/调试注入。
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

	staticNode := graph.Node{
		ID: "n1", Kind: "sample",
		Inputs: map[string]graph.InputRef{
			"URL":     {Value: json.RawMessage(`"https://prod.example.com"`)},
			"Headers": {Value: json.RawMessage(`{"X-Env":"prod"}`)},
			"extra":   {From: "$input", Field: "extra"},
		},
	}
	resolved := r.ResolveParams(staticNode, map[string]any{
		"URL":     "https://prod.example.com",
		"Headers": map[string]any{"X-Env": "prod"},
		"extra":   "val",
	}, nil)
	got, err := Params[sampleCfg](resolved)
	if err != nil || got.URL != "https://prod.example.com" || got.Headers["X-Env"] != "prod" {
		t.Fatalf("static resolve = %#v, err = %v", got, err)
	}

	inj := map[string]any{
		"URL":     "http://mock.local",
		"Headers": map[string]any{"X-Env": "mock"},
	}
	injectedResolved := r.ResolveParams(staticNode, inj, inj)
	injGot, err := Params[sampleCfg](injectedResolved)
	if err != nil || injGot.URL != "http://mock.local" || injGot.Headers["X-Env"] != "mock" {
		t.Fatalf("injected resolve = %#v, err = %v", injGot, err)
	}

	dynNode := graph.Node{
		ID: "n2", Kind: "sample",
		Inputs: map[string]graph.InputRef{
			"URL":        {From: "$input", Field: "url"},
			"TimeoutSec": {From: "$input", Field: "timeout"},
			"Query":      {From: "$input", Field: "q"},
		},
	}
	dynResolved := r.ResolveParams(dynNode, map[string]any{
		"URL":        "https://dyn.example.com",
		"TimeoutSec": int64(15),
		"Query":      map[string]any{"k": int64(1)},
	}, nil)
	dynGot, err := Params[sampleCfg](dynResolved)
	if err != nil || dynGot.URL != "https://dyn.example.com" || dynGot.TimeoutSec != 15 {
		t.Fatalf("dynamic resolve = %#v, err = %v", dynGot, err)
	}

	badResolved := r.ResolveParams(dynNode, map[string]any{
		"URL":        []any{"not-a-string"},
		"TimeoutSec": int64(15),
	}, nil)
	if _, err := Params[sampleCfg](badResolved); err == nil {
		t.Fatal("expected error when dynamic parameter has incompatible type")
	}
}

type validatedCfg struct {
	Max int `json:"Max,omitempty"`
}

func (c validatedCfg) Validate() error {
	if c.Max < 0 {
		return context.Canceled
	}
	return nil
}

// TestResolveParamsRunsInputValidator 确认运行期合入动态绑定或调试注入后仍执行 InputValidator.Validate()。
func TestResolveParamsRunsInputValidator(t *testing.T) {
	nSpec := stubNode{spec: WithParams(NodeSpec{
		ID: "validated", Name: "校验样例", Endpoints: DefaultEndpoints(),
	}, validatedCfg{})}
	r, err := NewRegistry(nSpec)
	if err != nil {
		t.Fatal(err)
	}
	n := graph.Node{ID: "v1", Kind: "validated"}
	bad := r.ResolveParams(n, map[string]any{"Max": int64(-1)}, map[string]any{"Max": int64(-1)})
	if err := CheckParams(bad); err == nil {
		t.Fatal("expected CheckParams error when InputValidator.Validate fails")
	}
}
