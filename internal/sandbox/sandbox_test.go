package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestDriver(t *testing.T) *Driver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(filepath.Join(t.TempDir(), "ws"), home)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExecBasic(t *testing.T) {
	d := newTestDriver(t)
	if !d.SandboxAvailable() {
		t.Fatalf("sandbox-exec 不可用：%s", d.SandboxError())
	}
	out, errOut, res := d.RunOutput(context.Background(), ExecRequest{
		Argv:    []string{"/bin/echo", "hello-isolation"},
		Timeout: 10 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("执行失败：%v stderr=%s", res.Err, errOut)
	}
	if !strings.Contains(out, "hello-isolation") {
		t.Fatalf("输出异常：%q", out)
	}
}

func TestWriteInsideWorkspace(t *testing.T) {
	d := newTestDriver(t)
	target := filepath.Join(d.Workspace, "inside.txt")
	_, errOut, res := d.RunOutput(context.Background(), ExecRequest{
		Argv:    []string{"/bin/sh", "-c", "printf ok > " + target},
		Timeout: 10 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("工作区内写入失败：%v stderr=%s", res.Err, errOut)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "ok" {
		t.Fatalf("文件内容异常：%v %q", err, string(b))
	}
}

func TestWriteOutsideDenied(t *testing.T) {
	d := newTestDriver(t)
	home, _ := os.UserHomeDir()
	target := filepath.Join(home, ".vmbra-sandbox-escape-test.txt")
	_ = os.Remove(target)
	_, _, res := d.RunOutput(context.Background(), ExecRequest{
		Argv:    []string{"/bin/sh", "-c", "printf bad > " + target},
		Timeout: 10 * time.Second,
	})
	if _, err := os.Stat(target); err == nil {
		_ = os.Remove(target)
		t.Fatal("沙箱逃逸：工作区外写入成功")
	}
	if res.Err == nil {
		t.Log("命令返回成功但未落盘，视为策略生效")
	}
}

func TestNetworkDeniedByDefault(t *testing.T) {
	d := newTestDriver(t)
	_, _, res := d.RunOutput(context.Background(), ExecRequest{
		Argv:    []string{"/usr/bin/curl", "-s", "-m", "5", "-o", "/dev/null", "https://example.com"},
		Timeout: 15 * time.Second,
	})
	if res.Err == nil {
		t.Fatal("网络默认应当被拒绝，但请求成功")
	}
}

func TestPathTraversalRejected(t *testing.T) {
	d := newTestDriver(t)
	if _, err := d.Resolve("../../etc/passwd"); err == nil {
		t.Fatal("路径穿越未被拦截")
	}
	if _, err := d.Resolve("projects/a.txt"); err != nil {
		t.Fatalf("合法路径被误判：%v", err)
	}
}

func TestSnapshotRestore(t *testing.T) {
	d := newTestDriver(t)
	if err := d.Write("projects/demo.txt", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "s.tar.gz")
	if _, err := d.Snapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if err := d.Write("projects/demo.txt", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := d.Restore(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	b, err := d.Read("projects/demo.txt", 1024)
	if err != nil || string(b) != "v1" {
		t.Fatalf("恢复失败：%v %q", err, string(b))
	}
}
