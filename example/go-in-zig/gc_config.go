package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"time"
)

// ============================================================
// GC 独占 CPU 配置
//
// 场景：Go 程序跑在高并发下，GC STW 抖动影响 P99。
// 思路：从 GOMAXPROCS 里扣一个核给 GC 背景 mark workers，
//       同时配合 debug.SetMemoryLimit 软限制防止 OOM。
// ============================================================

func init() {
	total := runtime.NumCPU()
	if total > 2 {
		runtime.GOMAXPROCS(total - 1)
	}

	// 存储 memory limit 供 gcReport 展示
	gcMemLimit = 12 * 1024 * 1024 * 1024 // 12 GiB
	debug.SetMemoryLimit(gcMemLimit)
}

var gcMemLimit int64

// gcReport 打印 GC 配置和运行时统计，供 main.go 调用
func gcReport() {
	total := runtime.NumCPU()
	gomaxprocs := runtime.GOMAXPROCS(0)

	fmt.Printf("=== GC-aware CPU config ===\n")
	fmt.Printf("  物理核:      %d\n", total)
	fmt.Printf("  GOMAXPROCS:  %d  (GC 留 1 核)\n", gomaxprocs)
	fmt.Printf("  MemoryLimit: %s\n", formatBytes(gcMemLimit))

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	fmt.Println("\n=== GC stats (2s interval) ===")
	for range 3 {
		<-ticker.C
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Printf("  GC count: %d  pause: %v  heap: %s\n",
			ms.NumGC, time.Duration(ms.PauseTotalNs), formatBytes(int64(ms.HeapAlloc)))
	}

	fmt.Println("\n=== 何时需要扣 GC 核 ===")
	fmt.Println("  - Go + CGO 混跑，CGO 调用占 C 线程，与 Go GC mark 抢 CPU")
	fmt.Println("  - 延迟敏感型微服务（P99 < 1ms），不希望 GC 暂停打扰任何请求")
	fmt.Println("  - 物理核 ≥ 4，扣 1 核比例可接受")
	fmt.Println()
	fmt.Println("  - 不是：普通 CRUD 服务，GC 占比 < 3%，扣核反而弱化吞吐")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
