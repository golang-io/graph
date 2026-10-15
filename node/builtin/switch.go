package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// Switch 分支节点。
const Switch = node.InnerNodePrefix + "switch"

// MaxSwitchCases 是 switch 种类声明的出口上限（Case0 .. Case15）；
// 实例只启用配置中出现的分支（见 SwitchInput.ActiveOutputs）。
const MaxSwitchCases = 16

// SwitchCase 多分支一条；ID 必须是 Case0..Case15，即出边 From 端点的本地名。
// 编辑器按顺序显示为 IF / ELIF n / ELSE；IsDefault 为未命中时的兜底（ELSE，恰好一条）。
type SwitchCase struct {
	ID        string `json:"ID"`
	Label     string `json:"Label,omitempty"`
	Expr      string `json:"Expr,omitempty"`
	IsDefault bool   `json:"IsDefault,omitempty"`
}

// SwitchInput 分支节点（没有输出，只选边）。条件表达式只能使用本节点输入名。
// Cases 为空时：对 Expr 求布尔，真走 Case0、假走 Case1（IF/ELSE）。
// Cases 非空时：按序求值，命中走对应 case.ID；须恰有一条 IsDefault。
type SwitchInput struct {
	Expr  string       `json:"Expr,omitempty" title:"条件表达式"`
	Cases []SwitchCase `json:"Cases,omitempty"`
}

// SwitchCaseID 返回第 i 个固定出口 ID（Case0 .. Case15）。
func SwitchCaseID(i int) string {
	if i < 0 || i >= MaxSwitchCases {
		return ""
	}
	return fmt.Sprintf("Case%d", i)
}

// SwitchCaseEndpoints 返回 switch 的全部固定出端点本地名（Case0..Case15）。
func SwitchCaseEndpoints() []string {
	out := make([]string, MaxSwitchCases)
	for i := range MaxSwitchCases {
		out[i] = SwitchCaseID(i)
	}
	return out
}

func validSwitchCaseID(id string) bool {
	for i := range MaxSwitchCases {
		if id == SwitchCaseID(i) {
			return true
		}
	}
	return false
}

func (c SwitchInput) Validate() error {
	if len(c.Cases) == 0 {
		return graph.ValidateExpr(c.Expr)
	}
	if c.Expr != "" || len(c.Cases) > MaxSwitchCases {
		return fmt.Errorf("ambiguous or oversized switch config")
	}
	labels := map[string]bool{}
	defaults := 0
	for _, caseItem := range c.Cases {
		id := strings.TrimSpace(caseItem.ID)
		if !validSwitchCaseID(id) || labels[id] {
			return fmt.Errorf("invalid switch case %q (want Case0..Case%d)", caseItem.ID, MaxSwitchCases-1)
		}
		labels[id] = true
		if caseItem.IsDefault {
			defaults++
			if caseItem.Expr != "" {
				return fmt.Errorf("default case cannot have expression")
			}
			continue
		}
		if err := graph.ValidateExpr(caseItem.Expr); err != nil {
			return err
		}
	}
	if defaults != 1 {
		return fmt.Errorf("exactly one default switch case required")
	}
	return nil
}

// ActiveOutputs 返回实例启用的出口：Cases 为空时为 IF/ELSE 的 Case0、Case1，否则为各分支的 ID。
// Registry.Validate 据此拒绝连在未配置分支上的出边。
func (c SwitchInput) ActiveOutputs() []string {
	if len(c.Cases) == 0 {
		return []string{SwitchCaseID(0), SwitchCaseID(1)}
	}
	out := make([]string, 0, len(c.Cases))
	for _, caseItem := range c.Cases {
		out = append(out, strings.TrimSpace(caseItem.ID))
	}
	return out
}

func (c SwitchInput) Expressions() []node.Expression {
	exprs := []node.Expression{{Path: "Expr", Text: c.Expr}}
	for i, caseItem := range c.Cases {
		exprs = append(exprs, node.Expression{Path: fmt.Sprintf("Cases[%d].Expr", i), Text: caseItem.Expr})
	}
	return exprs
}

// SwitchNode 实现分支节点。
type SwitchNode struct{}

func (SwitchNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Switch,
		Name:        "分支",
		Description: "按表达式选择出边；默认 IF/ELSE，可追加 ELIF（最多 16 路）",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "git-branch",
		Tone:        "amber",
		Renderer:    "switch",
		Inline:      true,
		// 种类声明全部 16 个出口作为上限；实例实际启用的出口由 SwitchInput.ActiveOutputs 给出。
		Endpoints: node.EndpointSpec{
			Inputs:  []string{node.DefaultInput},
			Outputs: SwitchCaseEndpoints(),
		},
	}, SwitchInput{})
}

func (SwitchNode) Execute(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
	cfg, err := node.Params[SwitchInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	branch, err := EvalSwitch(cfg, in.Vars)
	if err != nil {
		return node.NodeOutput{}, err
	}
	return node.Continue(nil, branch), nil
}

// EvalSwitch 按 SwitchInput 与变量选择出边（返回 Case0..Case15）。
func EvalSwitch(cfg SwitchInput, vars map[string]any) (string, error) {
	evalBool := func(expr string) (bool, error) {
		v, err := graph.EvalExpr(expr, vars)
		if err != nil {
			return false, err
		}
		b, ok := v.(bool)
		if !ok {
			return false, fmt.Errorf("switch: expression must evaluate to bool, got %T", v)
		}
		return b, nil
	}
	if len(cfg.Cases) == 0 {
		if strings.TrimSpace(cfg.Expr) == "" {
			return "", fmt.Errorf("switch: Expr required")
		}
		ok, err := evalBool(cfg.Expr)
		if err != nil {
			return "", fmt.Errorf("switch: %w", err)
		}
		if ok {
			return SwitchCaseID(0), nil
		}
		return SwitchCaseID(1), nil
	}
	var fallback string
	for _, c := range cfg.Cases {
		id := strings.TrimSpace(c.ID)
		if !validSwitchCaseID(id) {
			return "", fmt.Errorf("switch: case ID %q invalid", c.ID)
		}
		if c.IsDefault {
			if fallback == "" {
				fallback = id
			}
			continue
		}
		if strings.TrimSpace(c.Expr) == "" {
			return "", fmt.Errorf("switch case %q: Expr required", id)
		}
		ok, err := evalBool(c.Expr)
		if err != nil {
			return "", fmt.Errorf("switch case %q: %w", id, err)
		}
		if ok {
			return id, nil
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("switch: no case matched and no default")
}
