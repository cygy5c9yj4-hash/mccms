package web

import (
	"strings"
	"testing"
)

// 金额折算的边界条件是这次改动的核心，单独锁住。
func TestAfdianOrderDays(t *testing.T) {
	cases := []struct {
		name        string
		amount      string
		months      int
		daysPerMon  int
		priceCents  int64
		wantDays    int
		wantNoteHas string
	}{
		{"未设月费_走旧逻辑", "30.00", 2, 31, 0, 62, "未设置月费"},
		{"正好一个月", "30.00", 1, 31, 3000, 31, "= 1 个月"},
		{"正好三个月", "90.00", 3, 31, 3000, 93, "= 3 个月"},
		// 只给整月：50 元 ÷ 30 元 = 1 个月，零头舍去。
		{"不足两月只给一个月", "50.00", 2, 31, 3000, 31, "= 1 个月"},
		{"差一分不足一个月", "29.99", 1, 31, 3000, 0, "未发放"},
		{"一分钱也是不足", "0.01", 1, 31, 3000, 0, "未发放"},
		// 兑换码 / 赠送：爱发电文档说明此时 total_amount 为 0.00。
		{"兑换码订单按方案月数", "0.00", 1, 31, 3000, 31, "兑换/赠送"},
		{"兑换码订单多月", "0.00", 3, 31, 3000, 93, "兑换/赠送"},
		{"金额为空按兑换处理", "", 1, 31, 3000, 31, "兑换/赠送"},
		{"月费带小数", "7.50", 1, 31, 250, 93, "= 3 个月"},
		{"浮点误差不吞整月", "30.00", 1, 31, 3000, 31, "= 1 个月"},
		{"金额非法按兑换处理", "abc", 1, 31, 3000, 31, "兑换/赠送"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			days, note := afdianOrderDays(c.amount, c.months, c.daysPerMon, c.priceCents)
			if days != c.wantDays {
				t.Fatalf("days = %d, want %d (note=%q)", days, c.wantDays, note)
			}
			if !strings.Contains(note, c.wantNoteHas) {
				t.Fatalf("note = %q, want it to contain %q", note, c.wantNoteHas)
			}
		})
	}
}

// 30 元月费下，3000 分 / 3000 分必须精确等于 1，不能被浮点误差算成 0。
func TestAfdianOrderDaysNoFloatDrift(t *testing.T) {
	for _, amount := range []string{"30", "30.0", "30.00", "30.000"} {
		days, _ := afdianOrderDays(amount, 1, 31, 3000)
		if days != 31 {
			t.Fatalf("amount %q: days = %d, want 31", amount, days)
		}
	}
}

func TestParseYuanToCents(t *testing.T) {
	cases := map[string]int64{
		"":      0,
		"0":     0,
		"0.00":  0,
		"1":     100,
		"30":    3000,
		"30.5":  3050,
		"29.99": 2999,
		"abc":   0,
		"-5":    0,
		" 12 ":  1200,
	}
	for in, want := range cases {
		if got := parseYuanToCents(in); got != want {
			t.Errorf("parseYuanToCents(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNormalizeMonthPrice(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"0", "", false},
		{"30", "30.00", false},
		{"30.5", "30.50", false},
		{"30.555", "30.56", false},
		{"abc", "", true},
		{"-1", "", true},
	}
	for _, c := range cases {
		got, err := normalizeMonthPrice(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeMonthPrice(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeMonthPrice(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeMonthPrice(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// afdianOrderFromItem 必须把折算结果写进订单，并且仍然只接受 status=2 的订单。
func TestAfdianOrderFromItemWithPrice(t *testing.T) {
	item := map[string]any{
		"order_id":     "o1",
		"out_trade_no": "t1",
		"user_id":      "u1",
		"month":        2,
		"total_amount": "50.00",
		"status":       2,
	}
	ord := afdianOrderFromItem(item, 31, 3000)
	if ord == nil {
		t.Fatal("expected order, got nil")
	}
	if ord.Days != 31 {
		t.Fatalf("Days = %d, want 31 (50 元 ÷ 30 元 只给一个月)", ord.Days)
	}
	if !strings.Contains(ord.DaysNote, "= 1 个月") {
		t.Fatalf("DaysNote = %q", ord.DaysNote)
	}
	if ord.Amount != "50.00" {
		t.Fatalf("Amount = %q, want 50.00", ord.Amount)
	}

	// 不足一个月的订单也要记录下来，但天数为 0，后续 LinkOrder 会拒绝发放。
	item["total_amount"] = "10.00"
	ord = afdianOrderFromItem(item, 31, 3000)
	if ord == nil {
		t.Fatal("expected order recorded even when amount is too low")
	}
	if ord.Days != 0 {
		t.Fatalf("Days = %d, want 0", ord.Days)
	}

	// 非成功订单直接丢弃，绝不发放。
	item["status"] = 1
	if ord := afdianOrderFromItem(item, 31, 3000); ord != nil {
		t.Fatal("status != 2 should be dropped")
	}
}