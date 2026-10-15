package kernel

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
)

// Activity 是一次需要在 Apply 之外执行的活动节点调用。
// 引擎收到 ExecuteActivity 并把 Run 置为 RUNNING 后，由 NewActivity 生成，在 Apply 之外
// （不持有 Task 的写回顺序、不在事务内）调用 Run 执行，再用 Done 生成 ActivityDone 交回内核。
// 与 Apply 分离，供 immediate、durable 与第三方引擎复用；节点执行器的 IO 只发生在 Run 中。
//
// Activity is one activity-node invocation executed outside Apply.
type Activity struct {
	Node   graph.Node
	Vars   map[string]any // 投影后的输入（已合入调试注入的值）
	Params any            // 编译期解码并冻结在 Compiled 中的节点静态参数结构体
	// Key 是准备调用时 Run 所在执行的身份：Run 把它交给执行器，幂等键为 RunID:Seq（设计文档 8.6）；
	// Done 用其中的 RunID/Seq/Attempt 填入 ActivityDone，供内核判断结果是否过期。调用方不应修改。
	Key     node.RunKey
	Timeout time.Duration // Policy.NodeTimeoutMS；0 表示不限

	exec    node.Executor
	failure error // 输入来源缺失（正常不会出现：进入节点时已检查，等待执行期间 Outputs 不变）
	lookup  func(graph.InputRef) (any, bool)
}

// NewActivity 为 Run 准备活动节点调用：按投影计划从 Run.Outputs 与 Task.Input 取输入、按输入名合入调试注入的值、
// 查找执行器；调试注入的 Failed 只在进入节点时判定（内核前置检查），这里不再处理。
// Run 不在活动节点上或执行器缺失时返回 error（图与注册表不一致，属于引擎缺陷）。
func NewActivity(c *Compiled, task *engine.Task, r *engine.Run) (*Activity, error) {
	n, ok := c.Nodes[r.NodeID]
	if !ok {
		return nil, fmt.Errorf("kernel: run %s: node %q not in graph", r.RunID, r.NodeID)
	}
	key := node.RunKey{TaskID: r.TaskID, RunID: r.RunID, Seq: r.Seq, Attempt: r.Attempt}
	if c.Inline[n.ID] {
		return nil, fmt.Errorf("kernel: run %s: node %q is inline", r.RunID, n.ID)
	}
	exec, ok := c.Registry.ExecutorOf(n.Kind)
	if !ok {
		return nil, fmt.Errorf("kernel: run %s: node kind %q has no executor", r.RunID, n.Kind)
	}
	input, outs := maps.Clone(task.Input), r.Outputs.Clone()
	vars, missing := c.project(n.ID, input, outs)
	inj := task.Policy.Debug.InjectFor(n.ID)
	if inj != nil {
		maps.Copy(vars, inj.Vars)
	}
	params := c.resolveParams(n, vars, inj)
	a := &Activity{Node: n, Vars: vars, Params: params, Key: key, exec: exec, failure: missing, lookup: lookup(input, outs)}
	if task.Policy.NodeTimeoutMS > 0 {
		a.Timeout = time.Duration(task.Policy.NodeTimeoutMS) * time.Millisecond
	}
	return a, nil
}

// Run 执行节点。超时以 context 截止时间实现，超时与执行器 panic 都作为普通失败返回（由内核按重试策略处理）。
// 返回值未经形态校验与规整，由 Apply 处理 ActivityDone 时完成。
func (a *Activity) Run(ctx context.Context) (out node.NodeOutput, err error) {
	defer func() {
		if p := recover(); p != nil {
			out, err = node.NodeOutput{}, fmt.Errorf("node executor panic: %v", p)
		}
	}()
	if a.failure != nil {
		return node.NodeOutput{}, a.failure
	}
	if a.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.Timeout)
		defer cancel()
	}
	return a.exec.Execute(ctx, node.NodeInput{
		NodeID: a.Node.ID,
		Vars:   maps.Clone(a.Vars),
		Params: a.Params,
		Key:    a.Key,
		Lookup: a.lookup,
	})
}

// Done 用执行结果生成交回内核的 ActivityDone；RunID、Seq、Attempt 取自 Key，即准备调用时 Run 所在的执行。
// 用法：kernel.Apply(c, task, runs, act.Done(act.Run(ctx)), env)。
func (a *Activity) Done(out node.NodeOutput, err error) ActivityDone {
	return ActivityDone{
		RunID: a.Key.RunID, Seq: a.Key.Seq, Attempt: a.Key.Attempt,
		Output: out, Err: err,
	}
}

// Normalize 把输入 / 输出 map 规整为 JSON 解码后的形态（整数 int64、对象 map[string]any、数组 []any），
// 使内存中的 immediate 与经过数据库的 durable 对同一值得到相同类型。
// 只序列化一次，并按 jsonx.Strict 的规则检查（重复键、整数越界、无法无损往返的小数）；不合格时返回 ErrInvalid。
func Normalize(m map[string]any) (map[string]any, error) {
	out, err := jsonx.CanonicalObject(m)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	return out, nil
}
