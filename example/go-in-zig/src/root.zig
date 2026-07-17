const std = @import("std");

// ── C-compatible structs (must match Go side exactly) ──
const OrderRequest = extern struct {
    order_id: i64,
    amount_cents: i64,
    platform_rate_bps: i64,
    shop_rate_bps: i64,
    affiliate_rate_bps: i64,
    delivery_fee_cents: i64,
    coupon_discount_cents: i64,
    options: i64,
};

const ProfitResult = extern struct {
    order_id: i64,
    gross_revenue_cents: i64,
    platform_share_cents: i64,
    shop_share_cents: i64,
    affiliate_share_cents: i64,
    net_profit_cents: i64,
    effective_rate_bps: i64,
    flags: i64,
};

// ── Bit flags ──
const FLAG_VIP      = 0x1;
const FLAG_PICKUP   = 0x2;
const FLAG_GROUP    = 0x4;

// ── Default rates (bps) ──
const RATE_PLATFORM  = 100; // 1%
const RATE_PLATFORM_VIP  = 60;  // VIP 0.6%
const RATE_PAYMENT  = 38;   // 0.38% 支付通道费
const RATE_PICKUP_DISCOUNT = 30; // 自取优惠 0.3%

// ── Main export: batch profit splitting ──
export fn zig_split_profits(input_ptr: [*]OrderRequest, output_ptr: [*]ProfitResult, count: usize) void {
    // Choose fastest path: block or straight
    if (count >= 64) {
        // Large batch: split into 64-element chunks
        var start: usize = 0;
        while (start < count) {
            const end = if (start + 64 > count) count else start + 64;
            split_profits_chunk(input_ptr, output_ptr, start, end);
            start = end;
        }
    } else {
        // Small batch: straight compute
        split_profits_chunk(input_ptr, output_ptr, 0, count);
    }
}

fn split_profits_chunk(input: [*]OrderRequest, output: [*]ProfitResult, start: usize, end: usize) void {
    var i: usize = start;
    while (i < end) : (i += 1) {
        output[i] = split_one(input[i]);
    }
}

// ── Core: single order profit split ──
fn split_one(req: OrderRequest) ProfitResult {
    const is_vip    = (req.options & FLAG_VIP) != 0;
    const is_pickup = (req.options & FLAG_PICKUP) != 0;
    const is_group  = (req.options & FLAG_GROUP) != 0;

    // ----- Step 1: 计算实收金额 -----
    var revenue: i64 = req.amount_cents;
    if (is_pickup) {
        revenue -= req.delivery_fee_cents; // 自取不收配送费
    } else {
        revenue += req.delivery_fee_cents; // 外卖加配送费
    }
    revenue -= req.coupon_discount_cents; // 券抵扣

    // ----- Step 2: 平台分润 -----
    var platform_rate: i64 = RATE_PLATFORM;
    if (is_vip)  { platform_rate = RATE_PLATFORM_VIP; }
    if (is_group){ platform_rate += 20; } // 拼单额外 0.2%

    const platform_share: i64 = @divTrunc(revenue * platform_rate, 10000);
    const payment_fee: i64 = @divTrunc(revenue * RATE_PAYMENT, 10000);

    // ----- Step 3: 门店分润 -----
    var shop_rate: i64 = req.shop_rate_bps;
    if (is_pickup){ shop_rate += RATE_PICKUP_DISCOUNT; } // 自取多给门店
    const shop_share: i64 = @divTrunc(revenue * shop_rate, 10000);

    // ----- Step 4: 推荐人分润 -----
    const affiliate_share: i64 = @divTrunc(revenue * req.affiliate_rate_bps, 10000);

    // ----- Step 5: 净利润 = 实收 - 平台 - 支付通道 - 门店 - 推荐人 -----
    const net_profit: i64 = revenue - platform_share - payment_fee - shop_share - affiliate_share;

    // ----- Step 6: 综合费率 -----
    const effective_rate: i64 = if (revenue > 0) @divTrunc((platform_share + payment_fee) * 10000, revenue) else 0;

    // ----- Step 7: 结果标志 -----
    var flags: i64 = 0;
    if (is_vip)    { flags |= 0x1; }
    if (is_pickup) { flags |= 0x2; }
    if (is_group)  { flags |= 0x4; }
    if (net_profit < 0) { flags |= 0x100; } // 亏损订单

    return ProfitResult{
        .order_id = req.order_id,
        .gross_revenue_cents = revenue,
        .platform_share_cents = platform_share,
        .shop_share_cents = shop_share,
        .affiliate_share_cents = affiliate_share,
        .net_profit_cents = net_profit,
        .effective_rate_bps = effective_rate,
        .flags = flags,
    };
}

// ── Version ──
export fn zig_version() [*:0]const u8 {
    return "go-in-zig v0.1.0  profit-split engine (zig 0.16.0)";
}
