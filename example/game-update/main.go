package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fndome/zp"
)

// ============================================================
// 游戏域类型
// ============================================================

// PlayerState 数据库里的玩家状态（模拟）
type PlayerState struct {
	UserId  uint64
	Gold    int64
	HP      int64
	Exp     int64
	Updated time.Time
}

// StatDelta 单次增量请求 — Submit 只传 ID，非常轻量
type StatDelta struct {
	UserId uint64
}

// BatchResult 批量处理结果
type BatchResult struct {
	Updated int
	Elapsed time.Duration
}

// ============================================================
// 模拟数据库（内存表）
// ============================================================

var dbStore = struct {
	mu      sync.RWMutex
	players map[uint64]*PlayerState
	updates atomic.Int64
}{
	players: map[uint64]*PlayerState{
		1: {UserId: 1, Gold: 1000, HP: 100, Exp: 0, Updated: time.Now()},
		2: {UserId: 2, Gold: 500, HP: 80, Exp: 120, Updated: time.Now()},
		3: {UserId: 3, Gold: 2000, HP: 60, Exp: 300, Updated: time.Now()},
	},
}

func dbQuery(ids []uint64) []PlayerState {
	dbStore.mu.RLock()
	defer dbStore.mu.RUnlock()
	var rows []PlayerState
	for _, id := range ids {
		if p, ok := dbStore.players[id]; ok {
			rows = append(rows, *p)
		}
	}
	return rows
}

func dbUpdate(states []PlayerState) int {
	dbStore.mu.Lock()
	defer dbStore.mu.Unlock()
	for _, s := range states {
		if p, ok := dbStore.players[s.UserId]; ok {
			p.Gold = s.Gold
			p.HP = s.HP
			p.Exp = s.Exp
			p.Updated = time.Now()
		}
	}
	dbStore.updates.Add(int64(len(states)))
	return len(states)
}

// ============================================================
// 核心：Batcher processor — 攒 ID，执行时查最新 + 合并 + 写回
// ============================================================

func newGameUpdateBatcher() *zp.Batcher[StatDelta, BatchResult] {
	return zp.NewBatcher(
		100,                // 攒 100 个玩家或
		3*time.Second,      // 等 3 秒
		func(ctx context.Context, batch []StatDelta) ([]BatchResult, error) {
			t0 := time.Now()

			// 1. 去重 — 1秒内可能同一个玩家被多次投递
			seen := make(map[uint64]struct{}, len(batch))
			var ids []uint64
			for _, d := range batch {
				if _, ok := seen[d.UserId]; !ok {
					seen[d.UserId] = struct{}{}
					ids = append(ids, d.UserId)
				}
			}

			// 2. 查最新状态（此刻的值，不是 3 秒前的）
			latest := dbQuery(ids)

			// 3. 模拟"合并增量" — 每次清一批怪，加金/HP/Exp
			goldGain := int64(len(batch) * 10)
			hpLoss := int64(-len(batch) * 2)

			for i := range latest {
				latest[i].Gold += goldGain
				latest[i].HP += hpLoss
				latest[i].Exp += int64(len(batch))
				if latest[i].HP < 0 {
					latest[i].HP = 0
				}
			}

			// 4. 批量写回
			updated := dbUpdate(latest)

			// 每个输入必须对应一个结果，数量必须与 batch 一致
			res := BatchResult{Updated: updated, Elapsed: time.Since(t0)}
			results := make([]BatchResult, len(batch))
			for i := range results {
				results[i] = res
			}
			return results, nil
		},
	)
}

// ============================================================
// 模拟游戏服务器 — 高并发提交玩家增量
// ============================================================

func main() {
	fmt.Println("=== 游戏服务器: 玩家状态批量更新 (zp.Batcher) ===")
	fmt.Println()

	batcher := newGameUpdateBatcher()
	defer batcher.Stop()

	// 模拟 10 秒游戏时间，每 200ms 一批战斗事件
	rand.Seed(time.Now().UnixNano())

	var wg sync.WaitGroup
	var submitCount atomic.Int64

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	done := time.After(10 * time.Second)

loop:
	for {
		select {
		case <-ticker.C:
			// 每 200ms 有 3~8 个玩家获得收益
			n := rand.Intn(6) + 3
			for j := 0; j < n; j++ {
				wg.Add(1)
				go func(uid uint64) {
					defer wg.Done()
					res, err := batcher.Submit(context.Background(), StatDelta{UserId: uid})
					if err != nil {
						fmt.Printf("  submit err: %v\n", err)
						return
					}
					submitCount.Add(1)
					_ = res // res 是 BatchResult，通常不需要关心
				}(uint64(rand.Intn(3) + 1)) // userId: 1,2,3
			}

		case <-done:
			break loop
		}
	}

	wg.Wait()

	fmt.Printf("\n总提交: %d 次 | 实际DB写入: %d 次\n",
		submitCount.Load(), dbStore.updates.Load())

	fmt.Println()

	// 最终状态
	dbStore.mu.RLock()
	for i := uint64(1); i <= 3; i++ {
		p := dbStore.players[i]
		fmt.Printf("  玩家 %d  金币:%d  HP:%d  经验:%d  最后更新:%s\n",
			p.UserId, p.Gold, p.HP, p.Exp, p.Updated.Format("15:04:05"))
	}
	dbStore.mu.RUnlock()
}
