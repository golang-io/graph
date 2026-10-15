package kernel

import (
	"fmt"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// NewTask 按启动请求构造根 Task（PENDING，尚未写入；运行输入已写入 Task.Input）。
// 启动前完成全部校验，任一失败返回 ErrInvalid、不创建 Task：
//   - c 必须由 req.PinnedGraph() 编译：图身份与请求顶层的 GraphID / GraphVersion 一致；
//   - 策略取值（engine.NormalizePolicy）；
//   - 调试选项校验（Compiled.CheckDebug）；
//   - 运行输入按 Graph.InputSchema 校验并规整（整数为 int64）。
//
// 引擎保存 Task 后调用 Apply(c, task, nil, Started{}, env) 开始执行。
//
// NewTask builds a PENDING root task; apply Started{} after saving it.
func NewTask(kind string, req engine.StartRequest, c *Compiled, env Env) (*engine.Task, error) {
	if pinned := req.PinnedGraph(); c.Graph.GraphID != pinned.GraphID || c.Graph.GraphVersion != pinned.GraphVersion {
		return nil, fmt.Errorf("%w: compiled graph %s@%d does not match request %s@%d",
			engine.ErrInvalid, c.Graph.GraphID, c.Graph.GraphVersion, pinned.GraphID, pinned.GraphVersion)
	}
	policy, err := engine.NormalizePolicy(req.Policy)
	if err != nil {
		return nil, err
	}
	if err := c.CheckDebug(policy.Debug); err != nil {
		return nil, err
	}
	if err := c.CheckInput(req.Input); err != nil {
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
		// 列取自图快照的身份，二者同源。
		GraphID: c.Graph.GraphID, GraphVersion: c.Graph.GraphVersion,
		Status: graph.PENDING, Policy: policy, Input: input, Graph: c.Graph,
		StartedAt: now, CreatedAt: now,
	}
	return task, nil
}
