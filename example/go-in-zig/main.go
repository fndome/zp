package main

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/fndome/zp"
)

/*
#cgo CFLAGS: -I${SRCDIR}/zig-out/include
#cgo LDFLAGS: -L${SRCDIR}/zig-out/lib -lgo_in_zig

#include <stdint.h>
#include <stdlib.h>

// 对应 Go 的 OrderRequest
typedef struct {
	int64_t order_id;
	int64_t amount_cents;
	int64_t platform_rate_bps;
	int64_t shop_rate_bps;
	int64_t affiliate_rate_bps;
	int64_t delivery_fee_cents;
	int64_t coupon_discount_cents;
	int64_t options;
} order_request_t;

// 对应 Go 的 ProfitResult
typedef struct {
	int64_t order_id;
	int64_t gross_revenue_cents;
	int64_t platform_share_cents;
	int64_t shop_share_cents;
	int64_t affiliate_share_cents;
	int64_t net_profit_cents;
	int64_t effective_rate_bps;
	int64_t flags;
} profit_result_t;

extern void zig_split_profits(order_request_t* input, profit_result_t* output, size_t count);
*/
import "C"

// ============================================================
// Business Domain Types
// ============================================================

// OrderRequest 订单利润拆分请求 — 一条订单 + 多维度费率配置
type OrderRequest struct {
	OrderId           int64 `json:"orderId"`
	AmountCents       int64 `json:"amountCents"`       // 订单金额（分）
	PlatformRateBps   int64 `json:"platformRateBps"`   // 平台抽成（bps, 100=1%）
	ShopRateBps       int64 `json:"shopRateBps"`       // 门店分成（bps）
	AffiliateRateBps  int64 `json:"affiliateRateBps"`  // 推荐人分成（bps）
	DeliveryFeeCents  int64 `json:"deliveryFeeCents"`  // 配送费（分）
	CouponDiscountCents int64 `json:"couponDiscountCents"` // 券抵扣（分）
	Options           int64 `json:"options"`           // 位标志: 0x1=VIP, 0x2=自取, 0x4=拼单
}

// ProfitResult 利润拆分结果
type ProfitResult struct {
	OrderId          int64 `json:"orderId"`
	GrossRevenueCents int64 `json:"grossRevenueCents"`  // 实收金额
	PlatformShareCents int64 `json:"platformShareCents"` // 平台分得
	ShopShareCents     int64 `json:"shopShareCents"`     // 门店分得
	AffiliateShareCents int64 `json:"affiliateShareCents"` // 推荐人分得
	NetProfitCents     int64 `json:"netProfitCents"`     // 净利润
	EffectiveRateBps   int64 `json:"effectiveRateBps"`   // 实际综合费率
	Flags              int64 `json:"flags"`              // 结果标志
}

func (o *OrderRequest) toC() C.order_request_t {
	return C.order_request_t{
		order_id:            C.int64_t(o.OrderId),
		amount_cents:        C.int64_t(o.AmountCents),
		platform_rate_bps:   C.int64_t(o.PlatformRateBps),
		shop_rate_bps:       C.int64_t(o.ShopRateBps),
		affiliate_rate_bps:  C.int64_t(o.AffiliateRateBps),
		delivery_fee_cents:  C.int64_t(o.DeliveryFeeCents),
		coupon_discount_cents: C.int64_t(o.CouponDiscountCents),
		options:              C.int64_t(o.Options),
	}
}

func fromCResult(c *C.profit_result_t) ProfitResult {
	return ProfitResult{
		OrderId:            int64(c.order_id),
		GrossRevenueCents:  int64(c.gross_revenue_cents),
		PlatformShareCents: int64(c.platform_share_cents),
		ShopShareCents:     int64(c.shop_share_cents),
		AffiliateShareCents: int64(c.affiliate_share_cents),
		NetProfitCents:     int64(c.net_profit_cents),
		EffectiveRateBps:   int64(c.effective_rate_bps),
		Flags:              int64(c.flags),
	}
}

// ============================================================
// CGO wrappers
// ============================================================

func zigSplitProfitsSingle(req OrderRequest) ProfitResult {
	var out C.profit_result_t
	in := req.toC()
	C.zig_split_profits(&in, &out, 1)
	return fromCResult(&out)
}

func zigSplitProfitsBatch(reqs []OrderRequest) []ProfitResult {
	if len(reqs) == 0 {
		return nil
	}
	ins := make([]C.order_request_t, len(reqs))
	outs := make([]C.profit_result_t, len(reqs))
	for i, r := range reqs {
		ins[i] = r.toC()
	}
	C.zig_split_profits(
		(*C.order_request_t)(unsafe.Pointer(&ins[0])),
		(*C.profit_result_t)(unsafe.Pointer(&outs[0])),
		C.size_t(len(reqs)),
	)
	results := make([]ProfitResult, len(reqs))
	for i := range outs {
		results[i] = fromCResult(&outs[i])
	}
	return results
}

// ============================================================
// Demo
// ============================================================

func main() {
	gcReport()

	fmt.Println("\n=== 订单利润拆分: Go (Batcher) → CGO → Zig ===")

	// 生成典型订单：不同金额、不同费率
	orders := []OrderRequest{
		{1, 650, 100, 800, 50, 199, 0, 0},           // 普通外卖
		{2, 2500, 100, 750, 100, 0, 500, 0},          // 大单+券
		{3, 1500, 200, 700, 50, 0, 0, 1},            // VIP订单
		{4, 425, 100, 850, 0, 0, 0, 6},              // 拼单自取
		{5, 8800, 100, 800, 50, 0, 200, 1},          // VIP大单+券
	}

	fmt.Println("\n--- 逐单调用 Zig ---")
	for _, o := range orders {
		r := zigSplitProfitsSingle(o)
		fmt.Printf("  #%d 金额:%d 平台:%d 门店:%d 净利:%d\n",
			r.OrderId, r.GrossRevenueCents, r.PlatformShareCents,
			r.ShopShareCents, r.NetProfitCents)
	}

	fmt.Println("\n--- xg.Batcher 批量调用 + 延迟对比 ---")
	benchmarkBatcher(1000, 20*time.Millisecond)
	fmt.Println()
	benchmarkBatcher(5000, 50*time.Millisecond)
}

// ============================================================
// Benchmark with Batcher
// ============================================================

func benchmarkBatcher(n int, maxWait time.Duration) {
	// 真实业务模拟：生成 N 个随机订单
	allOrders := make([]OrderRequest, n)
	for i := 0; i < n; i++ {
		allOrders[i] = OrderRequest{
			OrderId:          int64(i + 1),
			AmountCents:      425 + int64(i%50)*100,
			PlatformRateBps:  100 + int64(i%3)*50,
			ShopRateBps:      750 + int64(i%4)*50,
			AffiliateRateBps: int64(i%3) * 30,
			Options:          int64(i % 8),
		}
	}

	// 逐个 CGO 调用
	{
		start := time.Now()
		for _, o := range allOrders {
			zigSplitProfitsSingle(o)
		}
		elapsed := time.Since(start)
		fmt.Printf("  single: N=%d total=%v avg=%v\n", n, elapsed, elapsed/time.Duration(n))
	}

	// 一次性批量调用
	{
		start := time.Now()
		zigSplitProfitsBatch(allOrders)
		elapsed := time.Since(start)
		fmt.Printf("  batch:  N=%d total=%v avg=%v\n", n, elapsed, elapsed/time.Duration(n))
	}

	// xg.Batcher 自带攒批（模拟高并发）
	batcher := zp.NewBatcher(
		64,
		maxWait,
		func(ctx context.Context, batch []OrderRequest) ([]ProfitResult, error) {
			return zigSplitProfitsBatch(batch), nil
		},
	)
	defer batcher.Stop()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var latencies []time.Duration

	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(o OrderRequest) {
			defer wg.Done()
			t0 := time.Now()
			_, err := batcher.Submit(context.Background(), o)
			mu.Lock()
			latencies = append(latencies, time.Since(t0))
			mu.Unlock()
			if err != nil {
				fmt.Printf("  err: %v\n", err)
			}
		}(allOrders[i])
	}
	wg.Wait()
	total := time.Since(start)

	p50 := percentile(latencies, 50)
	p90 := percentile(latencies, 90)
	p99 := percentile(latencies, 99)

	fmt.Printf("  batcher: N=%d total=%v avg=%v P50=%v P90=%v P99=%v\n",
		n, total, total/time.Duration(n), p50, p90, p99)
}

func percentile(a []time.Duration, pct int) time.Duration {
	sortLatencies(a)
	idx := len(a) * pct / 100
	if idx >= len(a) {
		idx = len(a) - 1
	}
	return a[idx]
}

func sortLatencies(a []time.Duration) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
