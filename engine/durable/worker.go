package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"github.com/golang-io/graph/internal/jsonx"
	"gorm.io/gorm/clause"
)

// 调度模型（设计文档 8.2、8.3）：
//   - Run 行即任务队列，不设单独的队列表。worker 用索引 IdxRunClaim
//     (EngineKind, Status, AvailableAt) 扫描到期的 PENDING Run。
//   - 租约、失联次数与已保存的结果是本引擎的策略数据，位于 DurableRun（列 LeaseUntil、LeaseToken、LostCount、Result）。
//   - 领取用条件更新（CAS）：UPDATE ... SET Status=RUNNING, LeaseUntil, LeaseToken=LeaseToken+1
//     WHERE RunID AND Status=PENDING AND LeaseToken=读到的值 AND AvailableAt<=now；影响 1 行即领取成功。
//     领取不锁 Task 行，不与写回互相阻塞（写回对此的处理见 store.go）。
//   - 活动节点在事务外执行，期间每 LeaseDuration/3 续租（条件带 LeaseToken）；续租未命中说明
//     Run 已被取消、落选或租约已被回收，立即中断执行，结果因 Run 状态或令牌不一致被丢弃。
//   - 执行结束后分两步：先用单行条件更新把结果写入自己的 Run（DurableRun.Result，条件 RUNNING 且 LeaseToken 一致，不锁 Task 行），
//     再以 ActivityDone 写回（store.go 的 event）。第二步失败或进程在两步之间退出时结果不丢失，
//     恢复扫描在租约过期后直接应用它（recover.go），节点不会因此重新执行。
//   - 写回中新出现的 PENDING Run 通过 kickAll 唤醒本进程空闲的 worker。
//   - 领取与恢复扫描遇到数据库错误时按带抖动的指数退避等待，避免数据库故障期间高频空转。

// job 标识一个 Run 及其租约令牌，领取查询、lease、恢复扫描与执行共用；字段直接接收 tb_graph_run 的同名列。
// LeaseToken 的含义取决于来源：
//   - claim 查询读出的是领取前的值，只作为 lease 条件更新的比较值；
//   - lease 成功后返回的副本已加 1，即本次领取持有的令牌，续租与写回都要求 Run 的 LeaseToken 与之一致；
//   - 恢复扫描读出的是当前持有者的令牌，用于确认回收时 Run 未被重新领取。
//
// LeaseUntil 记录本次领取或最近一次成功续租写入的租约到期时刻（Unix 毫秒），供 heartbeat 在数据库报错时判断租约是否已过期。
type job struct {
	RunID      string `gorm:"column:RunID"`
	TaskID     string `gorm:"column:TaskID"`
	LeaseToken int64  `gorm:"column:LeaseToken"`
	LeaseUntil int64  `gorm:"-"`
}

// claimBatch 是一次领取查询读取的 PENDING Run 数；按顺序 lease，只保留第一个成功的。
const claimBatch = 8

// schedule 启动 worker 并按 LeaseDuration/3 运行恢复扫描，阻塞到 stop 取消且所有 worker 退出。
// 恢复扫描报错时按带抖动的指数退避延长下次扫描间隔，成功后恢复默认间隔。
// 由构造函数在后台启动，Close 等待它返回。
func (e *Engine) schedule() {
	var wg sync.WaitGroup
	for range e.opt.Workers {
		wg.Go(e.worker)
	}
	base := e.opt.LeaseDuration / 3
	timer := time.NewTimer(base)
	defer timer.Stop()

	failures := 0
	for {
		select {
		case <-e.stop.Done():
			wg.Wait()
			return
		case <-timer.C:
			delay := base
			if err := e.recoverOnce(); err != nil {
				if e.stop.Err() == nil {
					e.cfg.Fail(fmt.Errorf("durable: recover: %w", err))
				}
				failures++
				delay = backoff(base, failures)
			} else {
				failures = 0
			}
			timer.Reset(delay)
		}
	}
}

// worker 持续领取到期的 PENDING Run 并执行；已领取的 Run 总是执行完，stop 只阻止新的领取。
// 无可领取 Run 时等待 kick 唤醒或 PollInterval 到期；领取报错时按带抖动的指数退避等待（期间不响应 kick）。
func (e *Engine) worker() {
	timer := time.NewTimer(0)
	defer timer.Stop()

	failures := 0
	for e.stop.Err() == nil {
		j, err := e.claim()
		if j != nil {
			failures = 0
			e.process(j)
			select {
			case <-e.kick:
			default:
			}
			continue
		}
		delay := e.opt.PollInterval
		kick := e.kick
		if err != nil {
			if e.stop.Err() == nil {
				e.cfg.Fail(fmt.Errorf("durable: claim: %w", err))
			}
			failures++
			delay = backoff(delay, failures)
			kick = nil
		} else {
			failures = 0
		}
		timer.Reset(delay)
		select {
		case <-e.stop.Done():
			return
		case <-kick:
		case <-timer.C:
		}
	}
}

// backoff 返回第 failures 次连续失败（从 1 开始）的带抖动指数退避时长：
// 上限按 2^failures 倍增（不超过 [5s, 1m] 封顶值），在 [max(base, ceil/2), ceil) 内随机抖动，错开多 worker 的重试时刻。
func backoff(base time.Duration, failures int) time.Duration {
	limit := min(time.Minute, max(5*time.Second, base*16))
	ceil := min(base<<min(failures, 6), limit)
	floor := max(base, ceil/2)
	return floor + rand.N(max(ceil-floor, 1))
}

// claim 领取一个到期的 PENDING Run。
// 从候选批次的随机下标开始环状尝试 lease，降低多 worker 同时扫描同一批次时的 CAS 竞争。
func (e *Engine) claim() (*job, error) {
	now := e.cfg.Now().UnixMilli()
	var pending []job
	err := e.cfg.DB.Model(&DurableRun{}).Select("RunID", "TaskID", "LeaseToken").
		Where(map[string]any{"EngineKind": engine.KindDurable, "Status": graph.PENDING}).
		Where(clause.Lte{Column: engine.Col("AvailableAt"), Value: now}).
		Order(engine.Asc("AvailableAt")).Limit(claimBatch).Find(&pending).Error
	if err != nil {
		return nil, err
	}
	n := len(pending)
	if n == 0 {
		return nil, nil
	}
	start := rand.IntN(n)
	for i := range n {
		j, err := e.lease(pending[(start+i)%n], now)
		if err != nil {
			return nil, err
		}
		if j != nil {
			return j, nil
		}
	}
	return nil, nil
}

// lease 用条件更新领取 j 指向的 Run：PENDING → RUNNING，LeaseToken 加 1，写入租约到期时刻。
// j.LeaseToken 是领取前读到的令牌；成功时返回令牌加 1 后的 job，即本次领取持有的令牌。
// 执行归属只由 LeaseToken 判断（续租、保存结果、写回都以它为条件），不记录持有者。
// 未命中（已被其他 worker 领取、已被写回修改、尚未到期）或遇到可重试的锁冲突时返回 nil。
func (e *Engine) lease(j job, now int64) (*job, error) {
	until := now + e.opt.LeaseDuration.Milliseconds()
	res := e.cfg.DB.Model(&DurableRun{}).
		Where(map[string]any{"RunID": j.RunID, "Status": graph.PENDING, "LeaseToken": j.LeaseToken}).
		Where(clause.Lte{Column: engine.Col("AvailableAt"), Value: now}).
		Updates(map[string]any{"Status": graph.RUNNING,
			"LeaseUntil": until, "LeaseToken": j.LeaseToken + 1})
	if res.Error != nil {
		if engine.Retryable(res.Error) {
			return nil, nil
		}
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, nil
	}
	j.LeaseToken++
	j.LeaseUntil = until
	return &j, nil
}

// process 执行已领取的 Run 并完成提交后的处理；写回产生本 Task 新的到期 PENDING Run 时直接接力领取执行，
// 省去返回主循环重新做全表 SELECT claim 查询。
func (e *Engine) process(j *job) {
	for j != nil && e.stop.Err() == nil {
		b, err := e.execute(j)
		if err != nil {
			e.cfg.Fail(fmt.Errorf("durable: run %s: %w", j.RunID, err))
			return
		}
		if b == nil {
			return
		}
		e.settle(b)
		j = e.relay(b)
	}
}

// relay 从刚刚提交的 batch 中直接按 CAS 接力领取一个已到期的 PENDING Run；无可用 Run 或领取未命中时返回 nil。
func (e *Engine) relay(b *batch) *job {
	if e.stop.Err() != nil || len(b.runs) == 0 {
		return nil
	}
	now := e.cfg.Now().UnixMilli()
	for _, r := range b.runs {
		if r.Status != graph.PENDING || r.AvailableAt > now {
			continue
		}
		j, err := e.lease(job{RunID: r.RunID, TaskID: r.TaskID, LeaseToken: r.LeaseToken}, now)
		if err != nil {
			if e.stop.Err() == nil {
				e.cfg.Fail(fmt.Errorf("durable: relay: %w", err))
			}
			return nil
		}
		if j != nil {
			return j
		}
	}
	return nil
}

// execute 执行已领取的活动节点：事务外执行并续租，结束后先保存结果（saveResult），再写回应用它。
// 返回 nil batch 表示没有写回：Run 已不由本次领取持有，或 Close 到期中断了执行（Run 保持 RUNNING，由租约过期回收）。
// 活动节点无法准备调用（图与注册表不一致）属于引擎缺陷，写入 kernel.Fail 的结果，Task FAILED。
func (e *Engine) execute(j *job) (*batch, error) {
	// 续租从领取后立即开始：载入 Task、Run 与编译需要多次访问数据库，
	// 网络延迟较大时这段时间可能超过租约，若等到执行节点时才续租，Run 会在执行前就被回收。
	// stop 结束续租并等待续租 goroutine 退出，可重复调用。
	ctx, cancel := context.WithCancel(e.life)
	x := e.trackLocal(j, cancel)
	defer e.untrackLocal(j, x)
	beat := e.heartbeat(ctx, cancel, j)
	stop := func() { cancel(); <-beat }
	defer stop()

	t, err := engine.LoadTask(e.cfg.DB.WithContext(ctx), j.TaskID, true)
	if err != nil {
		return nil, err
	}
	var r DurableRun
	if err := e.cfg.DB.WithContext(ctx).Where(map[string]any{"RunID": j.RunID}).Take(&r).Error; err != nil {
		return nil, err
	}
	if r.Status != graph.RUNNING || r.LeaseToken != j.LeaseToken {
		return nil, nil
	}
	a, err := kernel.NewActivity(e.cfg.Nodes, t, &r.Run)
	if err != nil {
		stop()
		return e.event(context.Background(), j.TaskID, owned(j, func(*DurableRun) act { return act{fail: err} }))
	}

	out, runErr := a.Run(ctx)
	stop()
	if e.life.Err() != nil {
		return nil, nil
	}
	saved, err := e.saveResult(j, a.Done(out, runErr))
	if err != nil || !saved {
		// 保存失败时 Run 保持 RUNNING、没有结果，由租约过期回收（与进程退出相同）；未命中说明已不由本次领取持有。
		return nil, err
	}
	return e.event(context.Background(), j.TaskID, owned(j, applyResult))
}

// saveResult 把执行结果写入本次领取持有的 Run：单行条件更新（RUNNING 且 LeaseToken 一致），不锁 Task 行，
// 因此不与其他 worker 的写回竞争。返回 false 表示 Run 已不由本次领取持有（已取消、落选或租约已被回收），结果丢弃。
func (e *Engine) saveResult(j *job, d kernel.ActivityDone) (bool, error) {
	res := e.cfg.DB.Model(&DurableRun{}).
		Where(map[string]any{"RunID": j.RunID, "Status": graph.RUNNING, "LeaseToken": j.LeaseToken}).
		Select("Result").Updates(&DurableRun{Result: resultOf(d)})
	return res.RowsAffected == 1, res.Error
}

// applyResult 是写回 DurableRun.Result 的动作：以保存的结果构造 ActivityDone；没有结果时（已被其他写回应用）不写回。
func applyResult(r *DurableRun) act {
	if r.Result == nil {
		return act{}
	}
	return act{ev: doneOf(r)}
}

// resultOf 把执行结果转为 DurableRun.Result 的存储形式。输出无法序列化为 JSON 时改记为执行失败
// （与内核对活动节点非法输出的处理一致：按 MaxAttempts 重试，耗尽后走错误出口），避免结果永远写不进库。
func resultOf(d kernel.ActivityDone) *RunResult {
	res := &RunResult{Seq: d.Seq, Attempt: d.Attempt}
	raw, err := json.Marshal(d.Output)
	switch {
	case d.Err != nil:
		res.Error = d.Err.Error()
		if res.Error == "" {
			res.Error = "activity failed"
		}
	case err != nil:
		res.Error = fmt.Sprintf("node output is not JSON serializable: %v", err)
	default:
		res.Output = raw
	}
	return res
}

// doneOf 由 DurableRun.Result 还原 ActivityDone；Seq、Attempt 取自保存时的结果。
func doneOf(r *DurableRun) kernel.ActivityDone {
	res := r.Result
	d := kernel.ActivityDone{RunID: r.RunID, Seq: res.Seq, Attempt: res.Attempt}
	if res.Error != "" {
		d.Err = errors.New(res.Error)
		return d
	}
	if len(res.Output) > 0 {
		if err := jsonx.DecodeStored(res.Output, &d.Output); err != nil {
			d.Err = fmt.Errorf("decode stored result: %v", err)
		}
	}
	return d
}

// owned 返回只在 Run 仍由本次领取持有（RUNNING 且 LeaseToken 一致）时才执行 f 的 decide；
// 否则（已被取消、落选或租约已被回收）不写回，结果丢弃（设计文档 8.4 第 2 步）。
func owned(j *job, f func(r *DurableRun) act) decide {
	return func(_ *engine.Task, runs map[string]*DurableRun) act {
		r := runs[j.RunID]
		if r == nil || r.Status != graph.RUNNING || r.LeaseToken != j.LeaseToken {
			return act{}
		}
		return f(r)
	}
}

// heartbeat 每 LeaseDuration/3 续租一次，条件带 LeaseToken（设计文档 8.3）：
//   - 续租成功（RowsAffected == 1）时推进本地已知的租约到期时刻 leaseUntil；
//   - 续租未命中（Error == nil && RowsAffected == 0）说明租约已被收回或 Run 已离开 RUNNING，立即中断执行；
//   - 数据库访问报错（Error != nil）时上报 Observer.EngineError；若当前时刻已达到或超过本地 leaseUntil，
//     说明租约在库中已到期、其他进程随时可能通过恢复扫描重新领取，立即中断执行以避免重复执行外部副作用。
//
// 返回的 channel 在续租 goroutine 退出后关闭。
//
// heartbeat extends the lease every LeaseDuration/3 and cancels ctx if the lease is lost or expires after DB errors.
func (e *Engine) heartbeat(ctx context.Context, cancel context.CancelFunc, j *job) <-chan struct{} {
	done := make(chan struct{})
	leaseUntil := j.LeaseUntil
	if leaseUntil <= 0 {
		leaseUntil = e.cfg.Now().Add(e.opt.LeaseDuration).UnixMilli()
	}
	go func() {
		defer close(done)
		tick := time.NewTicker(e.opt.LeaseDuration / 3)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			now := e.cfg.Now()
			rem := time.Duration(leaseUntil-now.UnixMilli()) * time.Millisecond
			if rem <= 0 {
				cancel()
				return
			}
			nextUntil := now.Add(e.opt.LeaseDuration).UnixMilli()
			qctx, qcancel := context.WithTimeout(ctx, rem)
			expireTimer := time.AfterFunc(rem, cancel)
			res := e.cfg.DB.WithContext(qctx).Model(&DurableRun{}).
				Where(map[string]any{"RunID": j.RunID, "Status": graph.RUNNING, "LeaseToken": j.LeaseToken}).
				Update("LeaseUntil", nextUntil)
			expireTimer.Stop()
			qcancel()
			if res.Error != nil {
				if ctx.Err() != nil && e.stop.Err() != nil {
					return
				}
				e.cfg.Fail(fmt.Errorf("durable: heartbeat run %s: %w", j.RunID, res.Error))
				if e.cfg.Now().UnixMilli() >= leaseUntil || errors.Is(res.Error, context.DeadlineExceeded) {
					cancel()
					return
				}
				continue
			}
			if res.RowsAffected == 0 {
				cancel()
				return
			}
			leaseUntil = nextUntil
		}
	}()
	return done
}

// localExec 是本进程内一次执行中的活动节点。
type localExec struct {
	token  int64
	cancel context.CancelFunc
}

// trackLocal / untrackLocal / interruptLocal 维护本进程执行中的节点：Run 被取消、落选或租约被回收的写回提交后
// 立即中断执行（其他进程的执行由续租未命中中断）。按 LeaseToken 匹配，同一 Run 被重新领取后旧的中断不会误伤新的执行。
func (e *Engine) trackLocal(j *job, cancel context.CancelFunc) *localExec {
	x := &localExec{token: j.LeaseToken, cancel: cancel}
	e.local.Store(j.RunID, x)
	return x
}

func (e *Engine) untrackLocal(j *job, x *localExec) { e.local.CompareAndDelete(j.RunID, x) }

func (e *Engine) interruptLocal(runID string, token int64) {
	if v, ok := e.local.Load(runID); ok {
		if x := v.(*localExec); x.token == token {
			x.cancel()
		}
	}
}

// kickAll 通知本进程空闲的 worker 立即扫描（非阻塞）。
func (e *Engine) kickAll() {
	for range e.opt.Workers {
		select {
		case e.kick <- struct{}{}:
		default:
			return
		}
	}
}
