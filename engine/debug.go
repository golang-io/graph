package engine

import (
	"fmt"
	"slices"

	"github.com/golang-io/graph/internal/jsonx"
)

// DebugInject 调试时按输入名覆盖指定节点投影后的输入（Vars 的键为该节点 Inputs 中的输入名，
// 也可补充未声明来源的输入名）；Failed 为 true 时该节点直接按失败处理。
type DebugInject struct {
	Vars   map[string]any `json:"Vars,omitempty"`
	Failed bool           `json:"Failed,omitempty"`
	Error  string         `json:"Error,omitempty"`
}

// DebugOptions 是 TaskPolicy 中的调试选项，随 Task 持久化，两个引擎语义一致：
//   - Step：每进入一个节点前停住，debug_step 放行一个，debug_continue 关闭单步
//   - Breakpoints：进入这些节点前停住
//   - Injects：按节点 ID 覆盖输入或强制失败
type DebugOptions struct {
	Step        bool                   `json:"Step,omitempty"`
	Breakpoints []string               `json:"Breakpoints,omitempty"`
	Injects     map[string]DebugInject `json:"Injects,omitempty"`
}

// Clone 深拷贝调试选项；nil 返回 nil。
func (d *DebugOptions) Clone() *DebugOptions {
	if d == nil {
		return nil
	}
	out := &DebugOptions{Step: d.Step, Breakpoints: slices.Clone(d.Breakpoints)}
	if len(d.Injects) > 0 {
		out.Injects = make(map[string]DebugInject, len(d.Injects))
		for id, inj := range d.Injects {
			inj.Vars = cloneMap(inj.Vars)
			out.Injects[id] = inj
		}
	}
	return out
}

// Normalize 深拷贝调试选项，并将 Injects 中各节点的 Vars 规整为标准 JSON 形态（整数为 int64）；
// Vars 不可序列化或包含越界数字时返回 ErrInvalid。
//
// Normalize deep-copies DebugOptions and normalizes each Injects[id].Vars via jsonx.CanonicalObject.
func (d *DebugOptions) Normalize() (*DebugOptions, error) {
	if d == nil {
		return nil, nil
	}
	out := &DebugOptions{Step: d.Step, Breakpoints: slices.Clone(d.Breakpoints)}
	if len(d.Injects) > 0 {
		out.Injects = make(map[string]DebugInject, len(d.Injects))
		for id, inj := range d.Injects {
			vars, err := jsonx.CanonicalObject(inj.Vars)
			if err != nil {
				return nil, fmt.Errorf("%w: debug inject %s: %v", ErrInvalid, id, err)
			}
			inj.Vars = vars
			out.Injects[id] = inj
		}
	}
	return out, nil
}

// Active 报告是否启用了会停住执行的调试选项（单步或断点）；启用时 immediate 强制串行。
func (d *DebugOptions) Active() bool {
	return d != nil && (d.Step || len(d.Breakpoints) > 0)
}

// HasBreakpoint 报告节点是否在断点列表中。
func (d *DebugOptions) HasBreakpoint(nodeID string) bool {
	return d != nil && nodeID != "" && slices.Contains(d.Breakpoints, nodeID)
}

// InjectFor 返回节点的调试注入；没有调试选项或注入为空时返回 nil。
func (d *DebugOptions) InjectFor(nodeID string) *DebugInject {
	if d == nil || nodeID == "" {
		return nil
	}
	inj, ok := d.Injects[nodeID]
	if !ok || (!inj.Failed && len(inj.Vars) == 0) {
		return nil
	}
	return &inj
}
