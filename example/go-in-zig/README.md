# go-in-zig

Go ↔ Zig via CGO.  典型业务：订单利润拆分，含复杂费率规则。

## 结构

```
go-in-zig/
├── build.zig          # zig build → shared lib → go build → run
├── build.zig.zon      # Zig 包清单 (0.16.0)
├── main.go            # Go: OrderRequest / ProfitResult + xg.Batcher
├── src/
│   └── root.zig       # Zig: split_profits (single / batch / parallel)
└── README.md
```

## 业务类型

```
OrderRequest                    ProfitResult
├── OrderId                     ├── OrderId
├── AmountCents     (订单金额)    ├── GrossRevenueCents   (实收)
├── PlatformRateBps (平台费率bps)  ├── PlatformShareCents  (平台)
├── ShopRateBps     (门店费率)    ├── ShopShareCents      (门店)
├── AffiliateRateBps(推荐费率)   ├── AffiliateShareCents (推荐人)
├── DeliveryFeeCents(配送费)     ├── NetProfitCents      (净利)
├── CouponDiscountCents(券)     ├── EffectiveRateBps    (综合费率)
└── Options         (位标记)      └── Flags              (结果标志)
    VIP=0x1 自取=0x2 拼单=0x4        亏损=0x100
```

## 费率规则（Zig 计算）

| 规则 | 条件 | 影响 |
|------|------|------|
| 自取 | `options & 0x2` | 免配送费，门店+0.3% |
| VIP | `options & 0x1` | 平台费率 1%→0.6% |
| 拼单 | `options & 0x4` | 平台+0.2% |
| 券 | `couponDiscountCents > 0` | 实收抵扣 |
| 支付通道 | 始终 | 0.38% 通道费 |

## 一键运行

```bash
zig build run
# Step 1: zig → zig-out/lib/go_in_zig.dll
# Step 2: go build → zig-out/bin/go_in_zig_project.exe
# Step 3: run → 分润示例 + 性能对比
```

## Go ↔ Zig 集成方式对比

| 方案 | 调用开销 | 官方支持 | 原理 |
|------|---------|---------|------|
| CGO（当前使用） | ~100ns/次 | Go 官方 | `export fn` + cgo C 桥接，goroutine 需 park/unpark |
| `.syso` 直接链接 | ~5ns/次 | **无承诺** | Zig `build-obj` 出 `.o`，改名为 `.syso` 放 Go 同目录，Go 链接器合并 |
| WASM | ~500ns/次 | Go 官方 | 沙箱隔离，不适合热路径 |

### `.syso` 方案（非官方、不推荐）

```bash
# Zig 编译为目标文件
zig build-obj src/root.zig -target x86_64-linux-gnu

# 改名放 Go 源码同目录
mv src/root.o root_amd64.syso
```

```go
// Go 侧 — 无需 cgo，无需 import "C"
//go:linkname zigComputeBatch zig_compute_batch
func zigComputeBatch(input *int64, output *int64, count uint64)
```

**为什么不推荐：**
- Go 团队从未承诺 `//go:linkname` 对 C 符号的 ABI 稳定性
- `.syso` 依赖 Go 版本、OS、架构的组合，换一个环境可能直接 segfault
- 没有类型安全，全靠手动对齐 `extern struct` 的内存布局
- 一旦 Go 改链接行为或 ABI，不提供向前兼容

**工程上：CGO + `xg.Batcher` 攒批已经足够。** 100 次 CGO 调用的 10μs 开销，摊到 100 条请求上就是 100ns/条。与其冒兼容性风险搞 `.syso`，不如把攒批写好。
