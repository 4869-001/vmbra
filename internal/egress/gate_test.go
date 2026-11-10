package egress

import (
	"testing"
	"time"
)

func TestGateOpenAllowsUntilTTL(t *testing.T) {
	g := New(80 * time.Millisecond)
	if g.Allows() {
		t.Fatal("新建的闸门必须是关闭状态")
	}
	g.Open(Report{OK: true})
	if !g.Allows() {
		t.Fatal("检查通过后应放行")
	}
	snap := g.Snapshot()
	if snap.State != StateOpen || snap.RemainingMS <= 0 {
		t.Fatalf("快照应显示放行与剩余时间: %+v", snap)
	}
	time.Sleep(120 * time.Millisecond)
	if g.Allows() {
		t.Fatal("租约到期后必须失效")
	}
	if st := g.State(); st != StateClosed {
		t.Fatalf("过期后状态应为 closed，实际 %s", st)
	}
	if snap := g.Snapshot(); snap.RemainingMS != 0 || snap.Allowed {
		t.Fatalf("过期后的快照不应再显示剩余时间: %+v", snap)
	}
}

func TestGateCloseBlocksImmediately(t *testing.T) {
	g := New(time.Minute)
	g.Open(Report{OK: true})
	g.Close("用户关闭出网")
	if g.Allows() {
		t.Fatal("关闭后不得放行")
	}
	if snap := g.Snapshot(); snap.Reason != "用户关闭出网" {
		t.Fatalf("应保留关闭原因: %+v", snap)
	}
}

func TestGateFailCountsAndCloses(t *testing.T) {
	g := New(time.Minute)
	g.Open(Report{OK: true})
	g.Fail(Report{OK: false, Error: "出口不可达"}, "出口不可达")
	if g.Allows() {
		t.Fatal("失败后不得放行")
	}
	snap := g.Snapshot()
	if snap.Failures != 1 || snap.Report.Error != "出口不可达" {
		t.Fatalf("失败次数与证据应被记录: %+v", snap)
	}
	g.Open(Report{OK: true})
	if snap := g.Snapshot(); snap.Failures != 0 {
		t.Fatalf("恢复放行后失败计数应清零: %+v", snap)
	}
}

func TestGateInvalidate(t *testing.T) {
	g := New(time.Minute)
	g.Open(Report{OK: true})
	g.Invalidate("切换出口节点")
	if g.Allows() || g.State() != StateClosed {
		t.Fatal("配置变更后旧租约必须作废")
	}
}

func TestGateCheckingState(t *testing.T) {
	g := New(time.Minute)
	g.BeginCheck()
	if g.Allows() {
		t.Fatal("检查中不应放行")
	}
	if g.State() != StateChecking {
		t.Fatalf("状态应为 checking，实际 %s", g.State())
	}
}
