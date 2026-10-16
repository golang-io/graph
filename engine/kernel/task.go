package kernel

import (
	"fmt"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// NewTask 按启动请求校验图定义与参数并构造根 Task（PENDING，尚未写入；运行输入已写入 Task.Input）。
// 启动前完成全部校验，任一失败返回 ErrInvalid、不创建 Task：
//   - 图定义按 reg.Validate 校验（节点、边、拓扑、引用与内嵌子图）；
//   - 策略取值（engine.NormalizePolicy）；
//   - 调试选项校验（CheckDebug）；
//   - 运行输入按 Graph.InputSchema 校验并规整（整数为 int64）。
//
// 引擎保存 Task 后调用 Apply(reg, task, nil, Started{}, env) 开始执行。
//
// NewTask validates the graph and request and builds a PENDING root task; apply Started{} after saving it.
func NewTask(kind string, req engine.StartRequest, reg *node.Registry, env Env) (*engine.Task, error) {
	if reg == nil {
		return nil, fmt.Errorf("%w: node registry required", engine.ErrInvalid)
	}
	g := req.PinnedGraph()
	if err := reg.Validate(g); err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	policy, err := engine.NormalizePolicy(req.Policy)
	if err != nil {
		return nil, err
	}
	if err := CheckDebug(reg, g, policy.Debug); err != nil {
		return nil, err
	}
	if err := CheckInput(g.InputSchema, req.Input); err != nil {
		return nil, err
	}
	input, err := Normalize(req.Input)
	if err != nil {
		return nil, err
	}
	id := env.newID()
	ticket := strings.TrimSpace(req.TicketID)
	if ticket == "" {
		ticket = id
	}
	now := env.now()
	task := &engine.Task{
		TaskID: id, TicketID: ticket, EngineKind: kind,
		GraphID: g.GraphID, GraphVersion: g.GraphVersion,
		Status: graph.PENDING, Policy: policy, Input: input, Graph: g,
		StartedAt: now, CreatedAt: now,
	}
	return task, nil
}
