package kernel

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// Env 是一次 Apply 的环境：时间、ID 生成、编译与已加载的子图。内核不读时钟、不生成随机数，全部由 Env 提供，
// 因此相同的 Env 与入参必然得到相同结果（设计文档 5.1）。
// Env 的字段都只做内存计算：按 GraphID 读取子图需要 IO，不经过 Env 调用，而是由引擎在 Apply 之外加载、
// 放入 Graphs 后再调用 Apply（见 NeedGraph），所以 Apply 不会在引擎的写回事务内访问外部资源。
//
// Env supplies time, IDs, compilation and preloaded subgraphs to Apply; none of them performs IO.
type Env struct {
	// Now 是本次处理事件的时刻；为零值时使用 time.Now().UTC()（只用于不关心确定性的调用方）。
	Now time.Time
	// NewID 生成 Run 与子 Task 的 ID；为空时使用 NewID（UUID v7，按时间有序）。
	NewID func() string
	// Compile 编译图（按 GraphID 加载的子图、map 循环体等内嵌子图），通常为 Cache.Compile；
	// 为空时直接调用 Compile（不缓存）。
	Compile func(g graph.Graph) (*Compiled, error)
	// Graphs 是引擎已加载的子图：GraphID → 加载结果（图或加载错误）。
	// 节点按 GraphID 派生子 Task 而这里没有该 GraphID 时，Apply 在 Changes.NeedGraphs 中返回缺少的 GraphID。
	Graphs map[string]LoadedGraph
	// Awaiting 是父 Run 仍在等待的子 Task（RunID → Slot → TaskID），只读视图，由引擎在调用 Apply 前提供：
	// durable 按子 Task 行的 Awaited 查询构造，immediate 用内存中的等待关系。
	// 内核只读这里、通过 Changes.Released 与 StartChild 输出等待关系的增减。
	// Awaiting lists the child tasks each run still awaits; the engine provides it, the kernel only reads it.
	Awaiting map[string]map[string]string
}

// LoadedGraph 是引擎按 GraphID 加载子图的结果：成功时 Graph 带身份（GraphID、GraphVersion），失败时 Err 非空。
// 加载失败也要放入 Env.Graphs：内核据此让发起它的节点失败，而不是再次请求加载同一子图。
type LoadedGraph = engine.LoadedGraph

// NeedGraph 表示推进过程中缺少按 GraphID 引用的子图：GraphIDs 列出本次需要、而 Env.Graphs 中没有的子图。
// 内核在内部用它中止本次推进并填入 Changes.NeedGraphs（Apply 本身返回 nil error）；
// 事务型引擎（如 durable）也可用它从写回事务内带出 Changes.NeedGraphs 以触发回滚并在事务外加载。
// 一次事件推进可能先后请求多次子图加载（例如 fork 的多个分支各自按 GraphID 派生），次数不超过不同 GraphID 的个数。
//
// NeedGraph reports subgraphs Apply needs; the engine loads them outside Apply and retries the same event.
type NeedGraph struct {
	GraphIDs []string
}

func (e *NeedGraph) Error() string {
	return fmt.Sprintf("kernel: subgraphs not loaded: %s", strings.Join(e.GraphIDs, ", "))
}

// AsNeedGraph 判断 err 是否为 *NeedGraph；是则返回需要加载的 GraphID 列表。
func AsNeedGraph(err error) ([]string, bool) {
	var need *NeedGraph
	if errors.As(err, &need) {
		return need.GraphIDs, true
	}
	return nil, false
}

// NewID 生成按时间有序的 ID（UUID v7），Task 与 Run 共用；按 RunID 排序即按创建顺序。
func NewID() string { return uuid.NewV7().String() }

func (env Env) now() time.Time {
	if env.Now.IsZero() {
		return time.Now().UTC()
	}
	return env.Now
}

func (env Env) newID() string {
	if env.NewID == nil {
		return NewID()
	}
	return env.NewID()
}

// Apply 把一个事件应用到 Task 的当前状态上，返回状态改动和需要引擎执行的命令。
// 不修改入参；不做 IO；相同输入必然得到相同输出。
//
// runs 传入该 Task 的全部 Run（含终态 Run：Task 结束时需要读取 end 的输出与失败原因）。
// Task 已结束时任何事件都返回空改动、无命令、nil error，由引擎返回当前状态。
//
// 按 GraphID 引用的子图尚未在 Env.Graphs 中提供时，返回 Changes{NeedGraphs: ids}、nil 命令、nil error
// （此时没有状态改动，Empty() 为 true）；引擎在 Apply 之外加载这些子图后，用同一事件重新调用 Apply。
//
// error 只表示真正的错误，分两类（设计文档 5.5），两类都没有任何改动：
//   - 拒绝：包装 engine.ErrConflict / engine.ErrInvalid，只由 Pause / Resume 产生，引擎原样返回给调用方（IsRejection）；
//   - 内核缺陷：其余 error（如 Controller 返回非法形态），引擎改为保存 Fail 的结果。
//
// Apply applies one event to a task's state and returns the changes and commands.
func Apply(c *Compiled, task *engine.Task, runs []*engine.Run, ev Event, env Env) (Changes, []Command, error) {
	if task == nil {
		return Changes{}, nil, fmt.Errorf("kernel: task required")
	}
	if graph.IsTerminal(task.Status) {
		return Changes{}, nil, nil
	}
	if c == nil {
		return Changes{}, nil, fmt.Errorf("kernel: compiled graph required for task %s", task.TaskID)
	}
	st := newState(c, task, runs, env)
	var err error
	switch ev := ev.(type) {
	case Started:
		err = st.onStarted()
	case ActivityDone:
		err = st.onActivityDone(ev)
	case ChildDone:
		err = st.onChildDone(ev)
	case Resume:
		err = st.onResume(ev)
	case Pause:
		err = st.onPause()
	case Cancel:
		st.onCancel(ev)
	default:
		err = fmt.Errorf("kernel: unknown event %T", ev)
	}
	if ids, ok := AsNeedGraph(err); ok {
		return Changes{NeedGraphs: ids}, nil, nil
	}
	if err != nil {
		return Changes{}, nil, err
	}
	ch, cmds := st.finish()
	return ch, cmds, nil
}

// IsRejection 报告 Apply 的 error 是否为拒绝（控制事件前提不满足），而不是内核缺陷。
// 引擎据此决定：拒绝原样返回给调用方，内核缺陷改为保存 Fail 的结果。
// 用专门的包装类型判定，而不是 errors.Is(ErrInvalid)：内核缺陷（如节点输出无法规整）也可能包装 ErrInvalid。
func IsRejection(err error) bool {
	var r *rejection
	return errors.As(err, &r)
}

// rejection 包装拒绝类错误；Unwrap 保留 ErrConflict / ErrInvalid，HTTP 层仍按它们映射状态码。
type rejection struct{ err error }

func (r *rejection) Error() string { return r.err.Error() }
func (r *rejection) Unwrap() error { return r.err }

// reject 构造拒绝：sentinel 为 engine.ErrConflict 或 engine.ErrInvalid。
func reject(sentinel error, format string, args ...any) error {
	return &rejection{err: fmt.Errorf("%w: "+format, append([]any{sentinel}, args...)...)}
}

// state 是一次 Apply 的工作副本：Task 与全部 Run 都在开始时复制，之后只修改副本，因此不修改入参。
// 修改过的 Task / Run 记为 dirty，结束时由 finish 生成 Changes 与命令。
// 等待关系（哪个 Run 在等哪些子 Task）同样是工作副本 awaiting：由 Env.Awaiting 深复制而来，
// 本次的增减记入 released，结束时由 finish 输出为 Changes.Released。
type state struct {
	c   *Compiled // Fail 时为 nil：只做取消与结束，不访问图
	env Env
	now time.Time

	task      *engine.Task
	taskDirty bool

	runs  map[string]*engine.Run
	dirty []string // 按首次修改顺序的 RunID
	seen  map[string]bool

	awaiting     map[string]map[string]string // RunID → Slot → TaskID，本次等待关系的工作副本
	released     []string                     // 本次不再等待的子 TaskID（按释放顺序，已去重）
	releasedSeen map[string]bool

	children []StartChild // 本次发起的子 Task
	cancels  []string     // 本次需要级联取消的子 TaskID
	notify   *NotifyParent
}

func newState(c *Compiled, task *engine.Task, runs []*engine.Run, env Env) *state {
	st := &state{
		c: c, env: env, now: env.now(),
		task:         task.Clone(),
		runs:         make(map[string]*engine.Run, len(runs)),
		seen:         map[string]bool{},
		awaiting:     make(map[string]map[string]string, len(env.Awaiting)),
		releasedSeen: map[string]bool{},
	}
	for _, r := range runs {
		if r != nil {
			st.runs[r.RunID] = r.Clone()
		}
	}
	for runID, slots := range env.Awaiting {
		// 必须深复制：Apply 内会增删，入参的 map 属于引擎，不修改入参。
		st.awaiting[runID] = maps.Clone(slots)
	}
	return st
}

// awaited 返回 Run 在等待的子 Task（Slot → TaskID）；不在等待时返回 nil。返回的 map 只读。
func (st *state) awaited(runID string) map[string]string {
	return st.awaiting[runID]
}

// await 记录 Run 开始等待子 Task。
func (st *state) await(runID, slot, taskID string) {
	m := st.awaiting[runID]
	if m == nil {
		m = make(map[string]string)
		st.awaiting[runID] = m
	}
	m[slot] = taskID
}

// unawait 移除 Run 对指定 Slot 的等待，返回被等待的子 TaskID；之前没有等待时返回 ""。
// 移除的同时记入 released，由 finish 输出为 Changes.Released（去重，重复释放无副作用）。
func (st *state) unawait(runID, slot string) string {
	m := st.awaiting[runID]
	if m == nil {
		return ""
	}
	id, ok := m[slot]
	if !ok {
		return ""
	}
	delete(m, slot)
	if len(m) == 0 {
		delete(st.awaiting, runID)
	}
	if id != "" && !st.releasedSeen[id] {
		st.releasedSeen[id] = true
		st.released = append(st.released, id)
	}
	return id
}

func (st *state) nowMS() int64 { return st.now.UnixMilli() }

// touchTask 标记 Task 已修改。
func (st *state) touchTask() { st.taskDirty = true }

// save 标记 Run 已修改；首次修改时记录顺序。
func (st *state) save(r *engine.Run) {
	if !st.seen[r.RunID] {
		st.seen[r.RunID] = true
		st.dirty = append(st.dirty, r.RunID)
	}
}

// insert 加入新建的 Run。
func (st *state) insert(r *engine.Run) {
	st.runs[r.RunID] = r
	st.save(r)
}

// list 返回处于这些状态的 Run，按 RunID 升序（RunID 为 UUID v7 时即创建顺序），保证遍历顺序确定。
func (st *state) list(statuses ...graph.StatusCode) []*engine.Run {
	out := make([]*engine.Run, 0, len(st.runs))
	for _, r := range st.runs {
		if slices.Contains(statuses, r.Status) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(x, y *engine.Run) int { return strings.Compare(x.RunID, y.RunID) })
	return out
}

// finish 生成 Changes 与命令。没有任何改动时返回空 Changes、nil 命令。
//
// 有改动时：Task 未结束则按 Run 汇总状态（存在 PENDING / RUNNING 为 RUNNING，只剩 WAITING 为 WAITING），
// Version 加 1。命令按 StartChild、CancelChild、ExecuteActivity、NotifyParent 的顺序输出；
// 同一次 Apply 中发起又被取消的子 Task（如 fork 后另一分支使 Task 失败）两条命令都不输出。
func (st *state) finish() (Changes, []Command) {
	if !st.taskDirty && len(st.dirty) == 0 && len(st.released) == 0 {
		return Changes{}, nil
	}
	t := st.task
	if !graph.IsTerminal(t.Status) {
		if status := summarizeRuns(st.runs); status != "" {
			t.Status = status
		}
	}
	t.Version++
	ch := Changes{Task: t}
	for _, id := range st.dirty {
		ch.Runs = append(ch.Runs, st.runs[id])
	}
	ch.Released = filterReleased(st.released, st.children)
	return ch, collectCommands(st.children, st.cancels, ch.Runs, st.notify)
}

// summarizeRuns 按 Run 汇总未结束 Task 的状态；没有未结束的 Run 时返回空（保持原状态）。
// 纯函数，只读 runs，便于单测：存在 PENDING / RUNNING 为 RUNNING，只剩 WAITING 为 WAITING。
func summarizeRuns(runs map[string]*engine.Run) graph.StatusCode {
	waiting := false
	for _, r := range runs {
		switch r.Status {
		case graph.PENDING, graph.RUNNING:
			return graph.RUNNING
		case graph.WAITING:
			waiting = true
		}
	}
	if waiting {
		return graph.WAITING
	}
	return ""
}

// filterReleased 只保留本次之前已存在的等待：
// 同一次 Apply 内发起又被取消的子 Task 行从未落库（durable 不插入、immediate 不登记），
// 它的释放不必输出，避免引擎清除不存在的标记。
func filterReleased(released []string, children []StartChild) []string {
	started := make(map[string]bool, len(children))
	for _, sc := range children {
		started[sc.Task.TaskID] = true
	}
	var out []string
	for _, id := range released {
		if !started[id] {
			out = append(out, id)
		}
	}
	return out
}

// collectCommands 按固定顺序组装命令：
// StartChild（跳过同批次又被取消的）、CancelChild（跳过同批次新发起的）、
// PENDING Run 的 ExecuteActivity、最后的 NotifyParent。纯函数，只做过滤与排序。
func collectCommands(children []StartChild, cancels []string, runs []*engine.Run, notify *NotifyParent) []Command {
	started := make(map[string]bool, len(children))
	for _, sc := range children {
		started[sc.Task.TaskID] = true
	}
	canceled := make(map[string]bool, len(cancels))
	for _, id := range cancels {
		canceled[id] = true
	}
	var cmds []Command
	for _, sc := range children {
		if !canceled[sc.Task.TaskID] {
			cmds = append(cmds, sc)
		}
	}
	for _, id := range cancels {
		if !started[id] {
			cmds = append(cmds, CancelChild{ChildTaskID: id})
		}
	}
	for _, r := range runs {
		if r.Status == graph.PENDING {
			cmds = append(cmds, ExecuteActivity{
				RunID: r.RunID, Seq: r.Seq, Attempt: r.Attempt,
				NotBefore: time.UnixMilli(r.AvailableAt).UTC(),
			})
		}
	}
	if notify != nil {
		cmds = append(cmds, *notify)
	}
	return cmds
}
