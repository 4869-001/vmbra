package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 旧版单工作站状态必须平滑迁移成集合，且环境参数取自当时的全局配置，
// 否则用户切回来会发现语言/时区「自己变了」。
func TestOpenMigratesLegacyWorkstation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{
	  "workstation": {"name":"old-ws","state":"running","driver":"local","image":"debian-12-arm64",
	    "cpus":8,"memoryMB":8192,"diskGB":40,"createdAt":"2026-01-02T03:04:05Z"},
	  "settings": {"networkEnabled": true, "theme": "dark"},
	  "locale": {"lang":"en_US.UTF-8","encoding":"UTF-8","timezone":"America/Los_Angeles"},
	  "proxy": {"mode":"manual"},
	  "browser": {"useVpsEgress": false}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cur := st.Copy().Current()
	if cur.Name != "old-ws" {
		t.Fatalf("名称未迁移：%q", cur.Name)
	}
	if cur.Root != "workstation" {
		t.Fatalf("默认工作站的目录应沿用旧路径：%q", cur.Root)
	}
	if cur.CPUs != 8 || cur.MemoryMB != 8192 || cur.DiskGB != 40 {
		t.Fatalf("规格未迁移：%+v", cur)
	}
	if !cur.Network || cur.Lang != "en_US.UTF-8" || cur.TimeZone != "America/Los_Angeles" {
		t.Fatalf("环境参数未对齐全局配置：%+v", cur)
	}
	if cur.ProxyMode != "manual" || cur.UseVPS {
		t.Fatalf("代理/出口未对齐全局配置：%+v", cur)
	}

	// 状态里不应再写出旧字段，重新读取保持同一结果。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"workstation":`) {
		t.Fatal("旧字段仍在落盘")
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Copy().Current().Name; got != "old-ws" {
		t.Fatalf("二次读取结果漂移：%q", got)
	}
}

func TestWorkstationCollectionHelpers(t *testing.T) {
	s := Default()
	if len(s.Workstations) != 1 || s.ActiveID != "default" {
		t.Fatalf("默认状态应有一台工作站：%+v", s)
	}
	if got := s.Current(); got.ID != "default" {
		t.Fatalf("Current 取错：%+v", got)
	}
	if _, ok := s.Find("nope"); ok {
		t.Fatal("不应找到不存在的工作站")
	}

	s.Workstations = append(s.Workstations, Workstation{ID: "second", Name: "第二台"})
	if got, ok := s.Find("second"); !ok || got.Name != "第二台" {
		t.Fatalf("Find 失败：%+v %v", got, ok)
	}

	// ActiveID 指向不存在的项时回落到第一台，避免整机「没有当前工作站」。
	s.ActiveID = "missing"
	if got := s.Active(); got == nil || got.ID != "default" {
		t.Fatalf("Active 未回落：%+v", got)
	}
}
