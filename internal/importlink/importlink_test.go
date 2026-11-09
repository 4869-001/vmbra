package importlink

import (
	"strings"
	"testing"
)

func sample() Payload {
	return Payload{
		V:     Version,
		Name:  "客户 A",
		Tools: []string{"node", "Git", "node", "unknown"},
		VPS:   VPSSpec{Host: "203.0.113.10", Port: 2222, User: "root", Password: "pw-123456"},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	link, err := Encode(sample())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(link, LinkPrefix) {
		t.Fatalf("链接前缀不对：%s", link)
	}

	got, err := Decode(link)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.VPS.Host != "203.0.113.10" || got.VPS.Port != 2222 || got.VPS.Password != "pw-123456" {
		t.Fatalf("VPS 信息丢失：%+v", got.VPS)
	}
	// 未知工具被丢弃、重复项去重、大小写归一
	if len(got.Tools) != 2 || got.Tools[0] != "node" || got.Tools[1] != "git" {
		t.Fatalf("工具清单未归一：%v", got.Tools)
	}
}

func TestDecodeFromConsoleURL(t *testing.T) {
	link, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	url := ConsoleURL("http://127.0.0.1:7788", link)
	got, err := Decode(url)
	if err != nil {
		t.Fatalf("Decode(console url): %v", err)
	}
	if got.VPS.Host != "203.0.113.10" {
		t.Fatalf("解析结果不对：%+v", got.VPS)
	}
}

func TestDecodeJSONPayload(t *testing.T) {
	raw := `{"v":1,"vps":{"host":"vps.example.com","port":22,"user":"ubuntu"},"tools":["claude"]}`
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode(json): %v", err)
	}
	if got.VPS.User != "ubuntu" || len(got.Tools) != 1 {
		t.Fatalf("解析结果不对：%+v", got)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []Payload{
		{V: Version, VPS: VPSSpec{Host: "bad host", Port: 22, User: "root"}},
		{V: Version, VPS: VPSSpec{Host: "1.2.3.4", Port: 0, User: "root"}},
		{V: Version, VPS: VPSSpec{Host: "1.2.3.4", Port: 22, User: "bad user"}},
		{V: Version + 1, VPS: VPSSpec{Host: "1.2.3.4", Port: 22, User: "root"}},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("第 %d 个用例应当校验失败", i)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode("vmbra://import/not-base64!!"); err == nil {
		t.Fatal("非法输入应当报错")
	}
}
