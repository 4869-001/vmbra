package vps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// InstallKey 用一次性口令登录远端，把本机公钥追加到 authorized_keys。
//
// 完成这一步之后隧道、终端与校验都改用密钥（BatchMode），口令不再参与任何调用，
// 也不会写入状态文件。实现依赖 OpenSSH 的 SSH_ASKPASS_REQUIRE=force（macOS 12+
// 与 Linux 上的 OpenSSH 8.4+ 均支持），因此不需要 sshpass 之类的额外依赖。
func InstallKey(ctx context.Context, host string, port int, user, password, pubKey, knownHosts string) error {
	if !hostPattern.MatchString(host) {
		return errors.New("VPS 主机地址不合法")
	}
	if !userPattern.MatchString(user) {
		return errors.New("VPS 登录用户不合法")
	}
	if port <= 0 || port > 65535 {
		return errors.New("SSH 端口需在 1-65535 之间")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("未提供 SSH 口令，无法自动安装公钥")
	}
	pub := strings.TrimSpace(pubKey)
	if pub == "" || strings.ContainsAny(pub, "'\"\n\r") {
		return errors.New("公钥内容为空或包含非法字符")
	}

	tmp, err := os.MkdirTemp("", "vmbra-askpass-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	_ = os.Chmod(tmp, 0o700)

	pwFile := filepath.Join(tmp, "pw")
	if err := os.WriteFile(pwFile, []byte(password+"\n"), 0o600); err != nil {
		return err
	}
	askpass := filepath.Join(tmp, "askpass.sh")
	script := "#!/bin/sh\ncat " + shellQuote(pwFile) + "\n"
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		return err
	}

	if knownHosts == "" {
		knownHosts = os.DevNull
	}

	remote := installKeyRemoteCommand(pub)

	args := installKeyArgs(host, port, user, knownHosts, remote)

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)
	cmd.Stdin = nil // askpass 而不是终端读取口令
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.New("安装公钥超时：检查 VPS 地址、端口与防火墙")
		}
		if strings.Contains(text, "Permission denied") {
			return errors.New("口令被拒绝：请确认面板密码正确、且 sshd 允许口令登录")
		}
		return fmt.Errorf("安装公钥失败：%v %s", err, truncate(text, 240))
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// installKeyRemoteCommand 生成远端脚本：幂等地把公钥追加到 authorized_keys。
func installKeyRemoteCommand(pub string) string {
	return "{ mkdir -p ~/.ssh && chmod 700 ~/.ssh; " +
		"touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys; " +
		"grep -qF '" + pub + "' ~/.ssh/authorized_keys 2>/dev/null || echo '" + pub + "' >> ~/.ssh/authorized_keys; }"
}

// installKeyArgs 生成 ssh 参数；抽出来便于测试。
func installKeyArgs(host string, port int, user, knownHosts, remote string) []string {
	return []string{
		"-o", "PreferredAuthentications=password",
		"-o", "PubkeyAuthentication=no",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "ConnectTimeout=12",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(port),
		"-l", user,
		host,
		remote,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
