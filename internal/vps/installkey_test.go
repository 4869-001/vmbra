package vps

import (
	"context"
	"strings"
	"testing"
)

func TestInstallKeyArgs(t *testing.T) {
	remote := installKeyRemoteCommand("ssh-ed25519 AAAA test")
	args := installKeyArgs("203.0.113.10", 2222, "root", "/tmp/known_hosts", remote)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"PreferredAuthentications=password",
		"PubkeyAuthentication=no",
		"NumberOfPasswordPrompts=1",
		"UserKnownHostsFile=/tmp/known_hosts",
		"-p 2222",
		"-l root",
		"203.0.113.10",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少参数 %q：%s", want, joined)
		}
	}
	if !strings.Contains(remote, "authorized_keys") {
		t.Fatal("远端命令未写入 authorized_keys")
	}
}

func TestInstallKeyRejectsBadInput(t *testing.T) {
	cases := []struct {
		host, user, password, pub string
		port                      int
	}{
		{"bad host", "root", "pw", "ssh-ed25519 AAAA", 22},
		{"1.2.3.4", "bad user", "pw", "ssh-ed25519 AAAA", 22},
		{"1.2.3.4", "root", "", "ssh-ed25519 AAAA", 22},
		{"1.2.3.4", "root", "pw", "ssh-ed25519 AAAA", 0},
		{"1.2.3.4", "root", "pw", "", 22},
	}
	for i, c := range cases {
		if err := InstallKey(context.Background(), c.host, c.port, c.user, c.password, c.pub, ""); err == nil {
			t.Fatalf("第 %d 个用例应当报错", i)
		}
	}
}
