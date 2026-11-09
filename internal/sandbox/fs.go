package sandbox

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrPathEscapes 表示目标路径越出了工作站工作区。
var ErrPathEscapes = errors.New("路径越出工作站工作区，已拒绝访问")

// Resolve 将工作区相对路径解析为绝对路径，并阻止目录穿越。
func (d *Driver) Resolve(rel string) (string, error) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", ErrPathEscapes
		}
	}
	clean := filepath.Clean("/" + rel)
	abs := filepath.Join(d.Workspace, clean)
	rootClean := filepath.Clean(d.Workspace)
	if abs != rootClean && !strings.HasPrefix(abs, rootClean+string(os.PathSeparator)) {
		return "", ErrPathEscapes
	}
	// 校验软链接不会把访问引到工作区之外
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if resolved != rootClean && !strings.HasPrefix(resolved, rootClean+string(os.PathSeparator)) {
			return "", ErrPathEscapes
		}
	}
	return abs, nil
}

// FileEntry 是文件列表中的一项。
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"modTime"`
}

// List 列出工作区目录内容，目录在前、按名称排序。
func (d *Driver) List(rel string) ([]FileEntry, error) {
	abs, err := d.Resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	rootClean := filepath.Clean(d.Workspace)
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(abs, e.Name())
		rp, _ := filepath.Rel(rootClean, p)
		out = append(out, FileEntry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(rp),
			Dir:     e.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Read 读取工作区文件，超过 limit 字节时报错。
func (d *Driver) Read(rel string, limit int64) ([]byte, error) {
	abs, err := d.Resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("目标是目录，无法作为文件读取")
	}
	if limit > 0 && info.Size() > limit {
		return nil, fmt.Errorf("文件过大（%d 字节），超过预览上限 %d 字节", info.Size(), limit)
	}
	return os.ReadFile(abs)
}

// Write 写入工作区文件（自动创建父目录）。
func (d *Driver) Write(rel string, data []byte) error {
	abs, err := d.Resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// Save 从 reader 流式写入工作区文件。
func (d *Driver) Save(rel string, r io.Reader) (int64, error) {
	abs, err := d.Resolve(rel)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(abs)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

// Mkdir 在工作区创建目录。
func (d *Driver) Mkdir(rel string) error {
	abs, err := d.Resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Remove 删除工作区内的文件或目录。
func (d *Driver) Remove(rel string) error {
	abs, err := d.Resolve(rel)
	if err != nil {
		return err
	}
	if filepath.Clean(abs) == filepath.Clean(d.Workspace) {
		return errors.New("不允许删除工作区根目录")
	}
	return os.RemoveAll(abs)
}

// Stat 返回工作区路径信息。
func (d *Driver) Stat(rel string) (os.FileInfo, error) {
	abs, err := d.Resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}

// Snapshot 将工作区打包为 tar.gz 快照，返回文件大小。
func (d *Driver) Snapshot(ctx context.Context, outPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return 0, err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	n, werr := d.WriteSnapshot(ctx, f)
	cerr := f.Close()
	if werr != nil {
		// 快照不完整时直接丢弃，避免半成品被当成可用快照。
		_ = os.Remove(outPath)
		return 0, werr
	}
	if cerr != nil {
		_ = os.Remove(outPath)
		return 0, cerr
	}
	return n, nil
}

// WriteSnapshot 把工作区打包为 tar.gz 流式写入 w，返回写入字节数。
// 用于「导出工作区」这类不需要在磁盘上留中间文件的场景。
func (d *Driver) WriteSnapshot(ctx context.Context, w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	root := d.Workspace
	walkErr := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if entry.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		_, cpErr := io.Copy(tw, src)
		closeErr := src.Close()
		if cpErr != nil {
			return cpErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		return cw.n, walkErr
	}
	if err := tw.Close(); err != nil {
		return cw.n, err
	}
	if err := gz.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}

// countingWriter 统计已写出的字节数，便于导出接口回报大小。
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Restore 用快照覆盖工作区内容。
func (d *Driver) Restore(ctx context.Context, snapPath string) error {
	f, err := os.Open(snapPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	if err := os.RemoveAll(d.Workspace); err != nil {
		return err
	}
	if err := os.MkdirAll(d.Workspace, 0o755); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := d.Resolve(hdr.Name)
		if err != nil {
			continue // 跳过越界条目，避免快照投毒
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

// Reset 清空工作区，恢复到初始骨架。
func (d *Driver) Reset() error {
	if err := os.RemoveAll(d.Workspace); err != nil {
		return err
	}
	for _, dir := range []string{d.Workspace, filepath.Join(d.Workspace, "projects"), filepath.Join(d.Workspace, "scripts")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(d.Workspace, "README.md"),
		[]byte("# 工作站工作区\n\n该目录位于本机隔离工作站的私有文件系统中，可由“文件”页管理与宿主机共享。\n"), 0o644)
}

// DirSize 统计目录占用字节数（跳过不可读项）。
func DirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// CommandExists 检查宿主机是否存在某个可执行文件。
func CommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
