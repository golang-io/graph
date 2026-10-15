package immediate

import "sync"

// slots 是全局活动节点名额（Options.MaxActivities），限制本引擎同时执行的活动节点总数（设计文档 9.2）。
//
// Run 拿到名额后才由 PENDING 置为 RUNNING；名额不足时 Run 保持 PENDING，其 Task 进入等待队列（每个 Task 至多排一次）。
// 名额释放时直接交给队首的 Task（记入 task.granted）并唤醒它的处理循环，而不是放回空闲后让各 Task 竞争：
// 这样排队的 Task 按先到先得取得名额，不会被后来者持续抢走。
// task.queued、task.granted 只在持有 mu 时访问。
//
// slots bounds concurrently executing activities; a freed slot is handed to the first waiting task.
type slots struct {
	mu      sync.Mutex
	free    int
	waiters []*task
}

func newSlots(n int) *slots { return &slots{free: n} }

// acquire 为 t 取一个名额：先用已交给 t 的名额，再用空闲名额；都没有时把 t 排入等待队列并返回 false。
func (s *slots) acquire(t *task) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.granted > 0 {
		t.granted--
		return true
	}
	if s.free > 0 {
		s.free--
		return true
	}
	if !t.queued {
		t.queued = true
		s.waiters = append(s.waiters, t)
	}
	return false
}

// release 归还一个名额：有排队的 Task 时交给队首并唤醒它，否则放回空闲。
func (s *slots) release() {
	s.mu.Lock()
	if len(s.waiters) == 0 {
		s.free++
		s.mu.Unlock()
		return
	}
	w := s.waiters[0]
	s.waiters[0] = nil
	s.waiters = s.waiters[1:]
	w.queued = false
	w.granted++
	s.mu.Unlock()
	w.box.kick()
}

// leave 在 t 当前不再需要名额时调用（没有可派发的 Run、受单 Task 上限限制或 Task 已结束）：
// 移出等待队列，并把已交给它但未使用的名额转交给其他 Task。
func (s *slots) leave(t *task) {
	s.mu.Lock()
	if t.queued {
		t.queued = false
		for i, w := range s.waiters {
			if w == t {
				s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
				break
			}
		}
	}
	n := t.granted
	t.granted = 0
	s.mu.Unlock()
	for range n {
		s.release()
	}
}
