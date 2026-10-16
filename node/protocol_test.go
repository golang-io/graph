package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/golang-io/graph"
)

// stubNode 是只用于注册表测试的节点：种类描述由用例给出，Execute 什么也不做。
type stubNode struct{ spec NodeSpec }

func (n stubNode) Spec() NodeSpec { return n.spec }

func (stubNode) Execute(context.Context, NodeInput) (NodeOutput, error) { return NodeOutput{}, nil }

// protocolTestType 返回无参数、单入单出的测试节点。
func protocolTestType(id string) Node {
	return stubNode{spec: WithParams(NodeSpec{ID: id, Name: "测试活动", Endpoints: DefaultEndpoints()}, struct{}{})}
}

func TestNodeRegistryInvariants(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	if err := registry.Register(nil); err == nil || !strings.Contains(err.Error(), "executor or controller required") {
		t.Fatalf("nil type error = %v", err)
	}
	good := protocolTestType("ready_activity")
	if err := registry.Register(good); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(good); err == nil {
		t.Fatal("duplicate type accepted")
	}
	// Get 返回副本：改返回值不影响注册表。
	type labeledCfg struct {
		X string `json:"X,omitempty" title:"原值"`
	}
	labeled := stubNode{spec: WithParams(NodeSpec{
		ID: "copy_activity", Name: "测试活动",
		Endpoints: EndpointSpec{Inputs: []string{DefaultInput}, Outputs: []string{DefaultOutput}},
	}, labeledCfg{})}
	if err := registry.Register(labeled); err != nil {
		t.Fatal(err)
	}
	got, _ := registry.Get("copy_activity")
	got.InputSchema.Properties["X"].Title = "篡改"
	if again, _ := registry.Get("copy_activity"); again.InputSchema.Properties["X"].Title != "原值" {
		t.Fatalf("Get must return a copy of InputSchema, got %q", again.InputSchema.Properties["X"].Title)
	}
}

// countingNode 统计 Spec() 被调用的次数。
type countingNode struct {
	stubNode
	calls *int
}

func (n countingNode) Spec() NodeSpec { *n.calls++; return n.stubNode.Spec() }

// TestNodeRegistryCallsSpecOnce 确认 Spec() 只在 Register 时调用一次，之后的查询、校验与输出规则都读固定的描述。
func TestNodeRegistryCallsSpecOnce(t *testing.T) {
	calls := 0
	n := countingNode{stubNode: protocolTestType("counted").(stubNode), calls: &calls}
	r, err := NewRegistry(n)
	if err != nil {
		t.Fatal(err)
	}
	inst := graph.Node{ID: "c", Kind: "counted"}
	r.Get("counted")
	r.List()
	r.Inline("counted")
	if err := r.CheckOutput(inst, NodeOutput{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Spec() called %d times, want 1", calls)
	}
}

func TestNodeRegistryRejectsDuplicateType(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	spec := protocolTestType("once_activity")
	if err := registry.Register(spec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(spec); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestNodeRegistryListIsSafe(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	if err := registry.Register(protocolTestType("list_activity")); err != nil {
		t.Fatal(err)
	}
	list := registry.List()
	if len(list) != 1 || list[0].ID != "list_activity" {
		t.Fatalf("List = %+v", list)
	}
	list[0].Name = "mutated"
	if again := registry.List(); again[0].Name != "测试活动" {
		t.Fatalf("List must return copies, got %q", again[0].Name)
	}
}

func TestNodeRegistryConcurrentGet(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	if err := registry.Register(protocolTestType("concurrent_activity")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, ok := registry.Get("concurrent_activity"); !ok {
					t.Error("missing")
				}
			}
		}()
	}
	wg.Wait()
}

func TestNodeRegistryRequiresNameAndEndpoints(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	noName := stubNode{spec: WithParams(NodeSpec{
		ID: "no_name", Endpoints: EndpointSpec{Inputs: []string{DefaultInput}, Outputs: []string{DefaultOutput}},
	}, struct{}{})}
	if err := registry.Register(noName); err == nil || !strings.Contains(err.Error(), "requires name") {
		t.Fatalf("empty name error = %v", err)
	}
	noEndpoints := stubNode{spec: WithParams(NodeSpec{ID: "no_endpoints", Name: "x"}, struct{}{})}
	if err := registry.Register(noEndpoints); err == nil || !strings.Contains(err.Error(), "requires endpoints") {
		t.Fatalf("empty endpoints error = %v", err)
	}
}

// TestNodeRegistryRejectsMalformedEndpoints 确认重复端点与含空白的端点名不能注册。
func TestNodeRegistryRejectsMalformedEndpoints(t *testing.T) {
	registry := &Registry{nodes: map[string]Node{}, specs: map[string]NodeSpec{}}
	for _, outputs := range [][]string{{"ok", "ok"}, {" in0 "}} {
		bad := stubNode{spec: WithParams(NodeSpec{
			ID: "bad_endpoints", Name: "x", Endpoints: EndpointSpec{Inputs: []string{DefaultInput}, Outputs: outputs},
		}, struct{}{})}
		if err := registry.Register(bad); err == nil {
			t.Fatalf("outputs %q accepted", outputs)
		}
	}
}

func TestWithParamsRoundTrip(t *testing.T) {
	type cfg struct {
		Name string `json:"Name"`
	}
	spec := WithParams(NodeSpec{ID: "p", Name: "p", Endpoints: EndpointSpec{Inputs: []string{DefaultInput}, Outputs: []string{DefaultOutput}}}, cfg{Name: "x"})
	if len(spec.DefaultInputs) == 0 || spec.paramsDecode == nil {
		t.Fatal("DefaultInputs/paramsDecode required")
	}
	decoded, err := spec.decodeParams(map[string]graph.InputRef{"Name": {Value: json.RawMessage(`"y"`)}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.(cfg)
	if !ok || got.Name != "y" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

type stubCtrlNode struct {
	spec NodeSpec
}

func (s stubCtrlNode) Spec() NodeSpec { return s.spec }
func (s stubCtrlNode) Control(context.Context, ControlInput) (NodeOutput, error) {
	return NodeOutput{}, nil
}

// TestCheckOutputControllerOnlyFields 确认 State 与 EffectSpawn 仅允许实现 Controller 的节点种类返回。
func TestCheckOutputControllerOnlyFields(t *testing.T) {
	execNode := protocolTestType("plain_exec")
	ctrlNode := stubCtrlNode{spec: WithParams(NodeSpec{
		ID: "plain_ctrl", Name: "测试控制",
		Endpoints: EndpointSpec{Inputs: []string{DefaultInput}, Outputs: []string{DefaultOutput}},
	}, struct{}{})}
	r, err := NewRegistry(execNode, ctrlNode)
	if err != nil {
		t.Fatal(err)
	}
	execInst := graph.Node{ID: "e", Kind: "plain_exec"}
	ctrlInst := graph.Node{ID: "c", Kind: "plain_ctrl"}

	stateOut := Wait(json.RawMessage(`{"step":1}`))
	spawnOut := SpawnChildren([]Spawn{{GraphID: "child"}}, json.RawMessage(`{"step":1}`))
	spawnNoStateOut := SpawnChildren([]Spawn{{GraphID: "child"}}, nil)

	if err := r.CheckOutput(execInst, stateOut); err == nil || !strings.Contains(err.Error(), "State is only allowed for Controller") {
		t.Fatalf("executor returning State err = %v", err)
	}
	if err := r.CheckOutput(execInst, spawnNoStateOut); err == nil || !strings.Contains(err.Error(), "spawn effect is only allowed for Controller") {
		t.Fatalf("executor returning EffectSpawn err = %v", err)
	}
	if err := r.CheckOutput(ctrlInst, stateOut); err != nil {
		t.Fatalf("controller returning State err = %v", err)
	}
	if err := r.CheckOutput(ctrlInst, spawnOut); err != nil {
		t.Fatalf("controller returning EffectSpawn err = %v", err)
	}
}
