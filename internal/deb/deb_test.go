package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type tarEntry struct {
	name    string
	body    string
	mode    int64
	typeflg byte
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		flag := e.typeflg
		if flag == 0 {
			flag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: mode, Size: int64(len(e.body)), Typeflag: flag}
		if flag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compress(t *testing.T, name string, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch {
	case strings.HasSuffix(name, ".tar"):
		return raw
	case strings.HasSuffix(name, ".tar.gz"):
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
	case strings.HasSuffix(name, ".tar.xz"):
		xw, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := xw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := xw.Close(); err != nil {
			t.Fatal(err)
		}
	case strings.HasSuffix(name, ".tar.zst"):
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("未知压缩格式：%s", name)
	}
	return buf.Bytes()
}

// arMember 生成一个 ar 归档成员（头 60 字节 + 数据 + 偶数对齐）。
func arMember(name string, data []byte) []byte {
	hdr := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n", name, 0, 0, 0, "100644", len(data))
	out := []byte(hdr)
	out = append(out, data...)
	if len(data)%2 == 1 {
		out = append(out, '\n')
	}
	return out
}

func buildDeb(t *testing.T, dataName string, dataTar []byte, control string) []byte {
	t.Helper()
	controlTar := buildTar(t, []tarEntry{{name: "./control", body: control}})
	controlName := "control.tar.gz"

	out := []byte("!<arch>\n")
	out = append(out, arMember("debian-binary", []byte("2.0\n"))...)
	out = append(out, arMember(controlName, compress(t, controlName, controlTar))...)
	out = append(out, arMember(dataName, compress(t, dataName, dataTar))...)
	return out
}

func writeDeb(t *testing.T, name string, dataName string, entries []tarEntry, control string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".deb")
	if err := os.WriteFile(path, buildDeb(t, dataName, buildTar(t, entries), control), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractDataFormats(t *testing.T) {
	entries := []tarEntry{
		{name: "./usr/bin/demo", body: "demo-binary", mode: 0o755},
		{name: "./usr/share/doc/demo/README", body: "hello"},
		{name: "../evil.txt", body: "escaped"},
		{name: "/etc/passwd", body: "root:x"},
		{name: "./usr/lib/demo-link", typeflg: tar.TypeSymlink},
		{name: "./usr/lib/hard", typeflg: tar.TypeLink},
	}

	for _, format := range []string{"data.tar", "data.tar.gz", "data.tar.xz", "data.tar.zst"} {
		t.Run(format, func(t *testing.T) {
			debPath := writeDeb(t, "demo", format, entries, "Package: demo\nVersion: 1.0\n")
			dest := filepath.Join(t.TempDir(), "root")
			rep, err := ExtractData(context.Background(), debPath, dest)
			if err != nil {
				t.Fatalf("解包失败：%v", err)
			}
			if rep.Files != 2 {
				t.Fatalf("应写出 2 个普通文件，实际 %d（%+v）", rep.Files, rep)
			}
			if rep.Skipped != 4 {
				t.Fatalf("应跳过 4 个越界/链接条目，实际 %d（%+v）", rep.Skipped, rep)
			}

			body, err := os.ReadFile(filepath.Join(dest, "usr/bin/demo"))
			if err != nil || string(body) != "demo-binary" {
				t.Fatalf("文件内容不正确：%q %v", string(body), err)
			}
			for _, leaked := range []string{
				filepath.Join(dest, "..", "evil.txt"),
				filepath.Join(dest, "etc/passwd"),
				filepath.Join(dest, "usr/lib/demo-link"),
				filepath.Join(dest, "usr/lib/hard"),
			} {
				if _, err := os.Lstat(leaked); err == nil {
					t.Fatalf("不应写出越界条目：%s", leaked)
				}
			}
		})
	}
}

func TestControl(t *testing.T) {
	debPath := writeDeb(t, "demo", "data.tar.gz", []tarEntry{{name: "./usr/bin/demo", body: "x"}},
		"Package: demo\nVersion: 1.0\nArchitecture: arm64\n")
	ctl, err := Control(context.Background(), debPath)
	if err != nil {
		t.Fatal(err)
	}
	if ctl != "Package: demo\nVersion: 1.0\nArchitecture: arm64" {
		t.Fatalf("control 内容不正确：%q", ctl)
	}
}

func TestMembersRejectsNonAr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.deb")
	if err := os.WriteFile(path, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Members(path); err == nil {
		t.Fatal("非 ar 文件应当报错")
	}
	if _, err := ExtractData(context.Background(), path, t.TempDir()); err == nil {
		t.Fatal("非 ar 文件解包应当报错")
	}
}

func TestCleanName(t *testing.T) {
	bad := []string{"", "..", "../x", "/etc/passwd", "a/../../b", "a\\b"}
	for _, name := range bad {
		if _, err := cleanName(name); err == nil {
			t.Fatalf("应当拒绝路径：%q", name)
		}
	}
	ok := map[string]string{
		"./usr/bin/demo": "usr/bin/demo",
		"usr/share/doc":  "usr/share/doc",
		"./":             "",
	}
	for in, want := range ok {
		got, err := cleanName(in)
		if want == "" {
			if err == nil {
				t.Fatalf("%q 应当被拒绝", in)
			}
			continue
		}
		if err != nil || got != want {
			t.Fatalf("%q → %q (%v)，期望 %q", in, got, err, want)
		}
	}
}
