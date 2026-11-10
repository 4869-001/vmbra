package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/tasks"
)

// Snapshots 返回全部快照。
func (s *Service) Snapshots() []store.Snapshot {
	return s.Store.Copy().Snapshots
}

// CreateSnapshot 为工作区创建快照。
func (s *Service) CreateSnapshot(name string) (*tasks.Item, error) {
	if name == "" {
		name = "快照 " + time.Now().Format("01-02 15:04")
	}
	id := fmt.Sprintf("snap-%d", time.Now().Unix())
	path := s.SnapshotPath(id)
	return s.Tasks.Start("snapshot", "创建快照 "+name, func(ctx context.Context, it *tasks.Item) error {
		it.Infof("正在归档工作区：%s", s.Drv.Workspace)
		size, err := s.Drv.Snapshot(ctx, path)
		if err != nil {
			return fmt.Errorf("创建快照失败：%w", err)
		}
		snap := store.Snapshot{ID: id, Name: name, CreatedAt: time.Now().UTC(), SizeBytes: size}
		if err := s.Store.Update(func(st *store.State) {
			st.Snapshots = append([]store.Snapshot{snap}, st.Snapshots...)
		}); err != nil {
			return err
		}
		it.Infof("快照完成：%s（%.2f MB）", id, float64(size)/(1<<20))
		return nil
	}), nil
}

// RestoreSnapshot 用快照恢复工作区。
func (s *Service) RestoreSnapshot(id string) (*tasks.Item, error) {
	var snap store.Snapshot
	found := false
	for _, x := range s.Snapshots() {
		if x.ID == id {
			snap, found = x, true
			break
		}
	}
	if !found {
		return nil, errors.New("快照不存在")
	}
	if s.Store.Copy().Current().State == "running" {
		return nil, fmt.Errorf("%w：请先停止工作站再恢复快照", ErrConflict)
	}
	return s.Tasks.Start("restore", "恢复快照 "+snap.Name, func(ctx context.Context, it *tasks.Item) error {
		it.Infof("正在恢复：%s", snap.Name)
		if err := s.Drv.Restore(ctx, s.SnapshotPath(snap.ID)); err != nil {
			return fmt.Errorf("恢复失败：%w", err)
		}
		it.Infof("恢复完成，工作区已回到快照状态")
		return nil
	}), nil
}

// DeleteSnapshot 删除快照。
func (s *Service) DeleteSnapshot(id string) error {
	found := false
	err := s.Store.Update(func(st *store.State) {
		out := make([]store.Snapshot, 0, len(st.Snapshots))
		for _, sp := range st.Snapshots {
			if sp.ID == id {
				found = true
				continue
			}
			out = append(out, sp)
		}
		st.Snapshots = out
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("快照不存在")
	}
	_ = os.Remove(s.SnapshotPath(id))
	return nil
}

// ResetWorkspace 清空工作区（保留快照）。
func (s *Service) ResetWorkspace() (*tasks.Item, error) {
	if s.Store.Copy().Current().State == "running" {
		return nil, fmt.Errorf("%w：请先停止工作站再重置", ErrConflict)
	}
	return s.Tasks.Start("reset", "重置工作站", func(ctx context.Context, it *tasks.Item) error {
		it.Infof("正在清空工作区与家目录缓存")
		s.Drv.CloseAll()
		if err := s.Drv.Reset(); err != nil {
			return fmt.Errorf("重置失败：%w", err)
		}
		it.Infof("工作区已恢复到初始骨架，快照保持不变")
		return nil
	}), nil
}

// StorageInfo 汇总存储占用。
func (s *Service) StorageInfo() map[string]any {
	workspace := sandbox.DirSize(s.Drv.Workspace)
	home := sandbox.DirSize(s.Drv.Home)
	dataDir := sandbox.DirSize(s.Cfg.DataDir)
	snapshots := sandbox.DirSize(s.Cfg.SnapshotDir())
	var free, total uint64
	if f, t, err := diskUsage(s.Cfg.DataDir); err == nil {
		free, total = f, t
	}
	return map[string]any{
		"workspaceBytes": workspace,
		"homeBytes":      home,
		"dataDirBytes":   dataDir,
		"snapshotBytes":  snapshots,
		"freeBytes":      free,
		"totalBytes":     total,
		"dataDir":        s.Cfg.DataDir,
		"workspacePath":  s.Drv.Workspace,
		"snapshotDir":    s.Cfg.SnapshotDir(),
	}
}

// OpenWorkspaceInFinder 在 Finder 打开工作区。
func (s *Service) OpenWorkspaceInFinder() error {
	return s.MacOpenPath("")
}

// ExportWorkspace 把工作区打包为 tar.gz 流式写入 w，供控制台下载，返回字节数。
// 导出是只读操作，工作站运行中也可以执行。
func (s *Service) ExportWorkspace(ctx context.Context, w io.Writer) (int64, error) {
	return s.Drv.WriteSnapshot(ctx, w)
}
