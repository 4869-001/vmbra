// Package deb 解析 Debian 软件包（ar 归档），并把 data.tar.* 安全解包到工作区。
//
// 解包只做「展开」：不执行维护脚本、不写入系统目录、不还原权限位与所有者。
// 归档中的符号链接、硬链接与设备节点一律跳过，路径穿越条目直接丢弃，
// 保证解包结果始终落在目标目录内。
package deb

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// 解包规模上限，避免超大安装包把工作区撑满。
const (
	MaxEntries      = 200_000
	MaxTotalBytes   = 2 << 30 // 2 GiB
	MaxControlBytes = 512 << 10
)

// ErrUnsupportedCompression 表示系统不认识 data.tar 使用的压缩格式。
var ErrUnsupportedCompression = errors.New("不支持的压缩格式")

// Member 是 ar 归档中的一个成员。
type Member struct {
	Name   string
	Offset int64
	Size   int64
}

// Report 是一次解包的统计结果。
type Report struct {
	Entries int   `json:"entries"` // 归档条目总数
	Files   int   `json:"files"`   // 实际写出的文件数
	Bytes   int64 `json:"bytes"`   // 实际写出的字节数
	Skipped int   `json:"skipped"` // 跳过的不安全条目（越界路径、链接、设备等）
}

// Members 解析 ar 归档成员表。deb 包本身是 ar 归档。
func Members(debPath string) ([]Member, error) {
	f, err := os.Open(debPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil {
		return nil, fmt.Errorf("不是有效的 deb 包（读取 ar 头失败）：%w", err)
	}
	if string(magic) != "!<arch>\n" {
		return nil, errors.New("不是有效的 deb 包（ar 归档头不符）")
	}

	var out []Member
	offset := int64(len(magic))
	for {
		var hdr [60]byte
		n, err := io.ReadFull(f, hdr[:])
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 ar 成员头失败：%w", err)
		}
		if string(hdr[58:60]) != "`\n" {
			return nil, errors.New("ar 成员头损坏")
		}
		name := strings.TrimSpace(string(hdr[0:16]))
		name = strings.TrimSuffix(name, "/")
		sizeText := strings.TrimSpace(string(hdr[48:58]))
		size, err := strconv.ParseInt(sizeText, 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("ar 成员大小非法：%q", sizeText)
		}
		dataOff := offset + 60
		if name != "" {
			out = append(out, Member{Name: name, Offset: dataOff, Size: size})
		}
		offset = dataOff + size
		if size%2 == 1 { // ar 成员按偶数字节对齐
			offset++
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, errors.New("deb 包内没有任何成员")
	}
	return out, nil
}

// Control 读取 control.tar.* 中的 control 文件（纯文本元信息）。
func Control(ctx context.Context, debPath string) (string, error) {
	members, err := Members(debPath)
	if err != nil {
		return "", err
	}
	m, ok := findMember(members, "control.tar")
	if !ok {
		return "", errors.New("包内缺少 control.tar")
	}
	rc, err := openMember(debPath, m)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("control.tar 中没有 control 文件")
		}
		if err != nil {
			return "", fmt.Errorf("读取 control.tar 失败：%w", err)
		}
		if path.Base(strings.TrimPrefix(hdr.Name, "./")) != "control" {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, MaxControlBytes))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(body)), nil
	}
}

// ExtractData 找到包内的 data.tar.* 并解包到 dest 目录。
// 目标目录会被创建，已存在的同名文件会被覆盖。
func ExtractData(ctx context.Context, debPath, dest string) (Report, error) {
	members, err := Members(debPath)
	if err != nil {
		return Report{}, err
	}
	m, ok := findMember(members, "data.tar")
	if !ok {
		return Report{}, errors.New("包内缺少 data.tar")
	}
	rc, err := openMember(debPath, m)
	if err != nil {
		return Report{}, err
	}
	defer rc.Close()
	return extractTar(ctx, rc, dest)
}

// findMember 按前缀匹配成员（data.tar / data.tar.gz / data.tar.xz ...）。
func findMember(members []Member, prefix string) (Member, bool) {
	for _, m := range members {
		if m.Name == prefix || strings.HasPrefix(m.Name, prefix+".") {
			return m, true
		}
	}
	return Member{}, false
}

// openMember 打开成员并套上对应的解压器。
func openMember(debPath string, m Member) (io.ReadCloser, error) {
	f, err := os.Open(debPath)
	if err != nil {
		return nil, err
	}
	section := io.NewSectionReader(f, m.Offset, m.Size)

	switch {
	case strings.HasSuffix(m.Name, ".tar"):
		return &compoundReader{r: section, closers: []func() error{f.Close}}, nil
	case strings.HasSuffix(m.Name, ".tar.gz"), strings.HasSuffix(m.Name, ".tgz"):
		gz, err := gzip.NewReader(section)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("解压 %s 失败：%w", m.Name, err)
		}
		return &compoundReader{r: gz, closers: []func() error{gz.Close, f.Close}}, nil
	case strings.HasSuffix(m.Name, ".tar.bz2"):
		return &compoundReader{r: bzip2.NewReader(section), closers: []func() error{f.Close}}, nil
	case strings.HasSuffix(m.Name, ".tar.xz"):
		xr, err := xz.NewReader(section)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("解压 %s 失败：%w", m.Name, err)
		}
		return &compoundReader{r: xr, closers: []func() error{f.Close}}, nil
	case strings.HasSuffix(m.Name, ".tar.zst"):
		zr, err := zstd.NewReader(section)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("解压 %s 失败：%w", m.Name, err)
		}
		return &compoundReader{r: zr, closers: []func() error{func() error { zr.Close(); return nil }, f.Close}}, nil
	default:
		f.Close()
		return nil, fmt.Errorf("%w：%s", ErrUnsupportedCompression, m.Name)
	}
}

type compoundReader struct {
	r       io.Reader
	closers []func() error
}

func (c *compoundReader) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *compoundReader) Close() error {
	var first error
	for _, close := range c.closers {
		if err := close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// extractTar 逐条校验并写出归档内容。
func extractTar(ctx context.Context, r io.Reader, dest string) (Report, error) {
	root, err := filepath.Abs(dest)
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Report{}, err
	}

	var rep Report
	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rep, fmt.Errorf("读取 data.tar 失败：%w", err)
		}
		rep.Entries++
		if rep.Entries > MaxEntries {
			return rep, fmt.Errorf("归档条目过多（上限 %d）", MaxEntries)
		}

		name, err := cleanName(hdr.Name)
		if err != nil {
			rep.Skipped++
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if !within(root, target) {
			rep.Skipped++
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return rep, err
			}
		case tar.TypeReg:
			if hdr.Size < 0 || rep.Bytes+hdr.Size > MaxTotalBytes {
				return rep, fmt.Errorf("解包体积超出上限（%d 字节）", int64(MaxTotalBytes))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return rep, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return rep, err
			}
			n, cpErr := io.Copy(out, tr)
			closeErr := out.Close()
			rep.Bytes += n
			if cpErr != nil {
				return rep, cpErr
			}
			if closeErr != nil {
				return rep, closeErr
			}
			rep.Files++
		default:
			// 符号链接、硬链接、设备节点、FIFO 等一律跳过：
			// 它们既可能把后续写入引出工作区，也没有解包价值。
			rep.Skipped++
		}
	}
	return rep, nil
}

// cleanName 把归档内路径规范化为相对路径，拒绝绝对路径与穿越。
func cleanName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", errors.New("空路径")
	}
	if strings.ContainsAny(n, "\x00\\") {
		return "", errors.New("非法字符")
	}
	clean := path.Clean(n)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", errors.New("越界路径")
	}
	return clean, nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
