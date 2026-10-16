package builtin

import (
	"context"
	"fmt"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// Merge 汇合节点。
const Merge = node.InnerNodePrefix + "merge"

// MergeInput 多路入边收成一路出边。入端点固定为 In，允许多条入边；
// Mode 决定 any / all；Outputs[].First 声明汇合字段取值顺序。
type MergeInput struct {
	Mode    string            `json:"Mode,omitempty" title:"模式" enum:"any,all"`
	Outputs []MergeFirstField `json:"Outputs,omitempty"`
}

// MergeFirstField 是 Merge 节点声明的一个汇合字段：按顺序取第一个在本次运行中已产生该字段的来源。
type MergeFirstField struct {
	Name  string           `json:"Name"`
	First []graph.InputRef `json:"First"`
}

func (c MergeInput) Validate() error {
	if !node.ValidJoinMode(c.Mode) {
		return fmt.Errorf("Mode must be %s or %s", node.JoinAny, node.JoinAll)
	}
	for i, f := range c.Outputs {
		if !graph.ValidName(f.Name) || len(f.First) == 0 {
			return fmt.Errorf("Outputs[%d]: Name must be a literal name and First must not be empty", i)
		}
		// 同名字段在运行期会互相覆盖，定义期直接拒绝。
		for j := range i {
			if c.Outputs[j].Name == f.Name {
				return fmt.Errorf("Outputs[%d]: duplicate Name %q", i, f.Name)
			}
		}
		for j, ref := range f.First {
			if ref.From == "" {
				return fmt.Errorf("Outputs[%d].First[%d]: From required", i, j)
			}
		}
	}
	return nil
}

func (c MergeInput) HasOutput(field string) bool {
	if !graph.ValidName(field) {
		return false
	}
	for i := range c.Outputs {
		if c.Outputs[i].Name == field {
			return true
		}
	}
	return false
}

func (c MergeInput) DeclareOutputs() *jsonschema.Schema {
	names := make([]string, 0, len(c.Outputs))
	for _, f := range c.Outputs {
		names = append(names, f.Name)
	}
	return node.OutputSchema(names...)
}

func (c MergeInput) JoinMode() string {
	return node.NormalizeJoinMode(c.Mode)
}

func (c MergeInput) References() []node.NamedRef {
	var refs []node.NamedRef
	for _, f := range c.Outputs {
		for i, ref := range f.First {
			refs = append(refs, node.NamedRef{
				Path: fmt.Sprintf("output %q First[%d]", f.Name, i),
				Ref:  ref,
			})
		}
	}
	return refs
}

// MergeNode 实现汇合节点：等待由内核按 Mode（any / all）完成，Execute 只计算汇合字段。
type MergeNode struct{}

func (MergeNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Merge,
		Name:        "合并",
		Description: "显式选择任一路或等待全部路径；单入口 in，多条入边汇合",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "git-merge",
		Tone:        "violet",
		Renderer:    "merge",
		Inline:      true,
		Endpoints: node.EndpointSpec{
			Inputs:  []string{node.DefaultInput},
			Outputs: []string{node.DefaultOutput},
		},
	}, MergeInput{Mode: node.JoinAny})
}

// Execute 对每个汇合字段按 First 顺序取第一个本次运行中已产生的来源值。
// 走 any 时只有先到的分支产生了输出，First 列表用来在各分支同名字段之间做选择；
// 所有来源都未产生时报错，避免下游拿到缺失字段。
func (MergeNode) Execute(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
	cfg, err := node.Params[MergeInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	if len(cfg.Outputs) == 0 {
		return node.NodeOutput{}, nil
	}
	set := make(map[string]any, len(cfg.Outputs))
	for _, f := range cfg.Outputs {
		found := false
		for _, ref := range f.First {
			if v, ok := in.Lookup(ref); ok {
				set[f.Name], found = v, true
				break
			}
		}
		if !found {
			return node.NodeOutput{}, fmt.Errorf("merge output %q unavailable: none of its First sources has been executed", f.Name)
		}
	}
	return node.Continue(set, ""), nil
}
