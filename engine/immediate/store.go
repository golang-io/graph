package immediate

import (
	"fmt"

	"github.com/golang-io/graph"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// persist 在 Task 进入终态后，于一个事务内写入 Task 行与该 Task 的全部 Run（设计文档 9.3）。
// 运行期间不写库，因此这是该 Task 唯一的一次写入；按主键 upsert，重复写入结果相同。
// 写入失败只上报 Observer，不重试执行；结果仍可在内存保留期内通过 Snapshot / Watch 获取。没有数据库时跳过。
func (e *Engine) persist(tk *task) {
	if e.cfg.DB == nil {
		return
	}
	snap, _ := tk.view()
	if !graph.IsTerminal(snap.Task.Status) {
		return
	}
	err := e.cfg.DB.Transaction(func(tx *gorm.DB) error {
		task := snap.Task
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&task).Error; err != nil {
			return err
		}
		if len(snap.Runs) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(&snap.Runs, 100).Error
	})
	if err != nil {
		e.cfg.Fail(fmt.Errorf("immediate: save task %s: %w", tk.id, err))
	}
}
