package immediate

import (
	"sync"

	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
)

// message 是投递给 Task 处理循环的一条消息。
type message struct {
	ev kernel.Event
	// reply 非空时，处理循环应用事件后把回执或拒绝错误写入（容量 1，不阻塞处理循环）。
	reply chan reply
	// graphs 非空表示这是加载子图后重新投递的消息（Apply 曾返回 Changes.NeedGraphs）：处理循环先把它并入
	// 已加载的子图，再重新应用 ev；ev 的执行计数等首次投递时的处理不再重复。
	graphs map[string]kernel.LoadedGraph
	// fail 非空表示后台加载子图时发现内核重复请求已加载的子图（engine.LoadGraphs 返回错误），
	// 处理循环直接按内核缺陷结束 Task，不再重新应用 ev。
	fail error
}

type reply struct {
	rec engine.Receipt
	err error
}

// mailbox 是 Task 处理循环的事件队列，不限长度：投递方（活动节点 goroutine、其他 Task 的处理循环、
// 控制面调用）永不阻塞，因此父子 Task 互相投递 ChildDone / Cancel 时不会互相等待。
// 关闭后投递返回 false，消息被丢弃（Task 已结束，Apply 对任何事件都不会再有改动）。
//
// mailbox is an unbounded queue; senders never block.
type mailbox struct {
	mu     sync.Mutex
	items  []message
	closed bool
	signal chan struct{} // 容量 1：有新消息或需要重新派发时置位
}

func newMailbox() *mailbox { return &mailbox{signal: make(chan struct{}, 1)} }

// put 投递消息；mailbox 已关闭时返回 false。
func (m *mailbox) put(msg message) bool {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.items = append(m.items, msg)
	m.mu.Unlock()
	m.kick()
	return true
}

// kick 唤醒处理循环（非阻塞，多次唤醒合并为一次）：重试到期、拿到全局名额时使用。
func (m *mailbox) kick() {
	select {
	case m.signal <- struct{}{}:
	default:
	}
}

// take 取出当前全部消息（按投递顺序）。
func (m *mailbox) take() []message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.items
	m.items = nil
	return out
}

// close 关闭 mailbox 并返回尚未处理的消息，由调用方回复其中等待回执的调用者。
func (m *mailbox) close() []message {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	out := m.items
	m.items = nil
	return out
}
