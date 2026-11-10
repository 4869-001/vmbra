package service

import "testing"

func TestLocaleForCountry(t *testing.T) {
	cases := []struct {
		cc      string
		posix   string
		browser string
	}{
		{"US", "en_US.UTF-8", "en-US"},
		{"jp", "ja_JP.UTF-8", "ja-JP"},
		{"GB", "en_GB.UTF-8", "en-GB"},
		{"CN", "zh_CN.UTF-8", "zh-CN"},
		{"TW", "zh_TW.UTF-8", "zh-TW"},
		{"DE", "de_DE.UTF-8", "de-DE"},
		{"", "en_US.UTF-8", "en-US"},
		{"ZZ", "en_US.UTF-8", "en-US"},
	}
	for _, c := range cases {
		posix, browser := localeForCountry(c.cc)
		if posix != c.posix || browser != c.browser {
			t.Fatalf("localeForCountry(%q) = (%q,%q)，期望 (%q,%q)", c.cc, posix, browser, c.posix, c.browser)
		}
	}
}

func TestTimezoneForCountry(t *testing.T) {
	cases := map[string]string{
		"US": "America/Los_Angeles",
		"JP": "Asia/Tokyo",
		"GB": "Europe/London",
		"CN": "Asia/Shanghai",
		"ZZ": "America/Los_Angeles",
	}
	for cc, want := range cases {
		if got := timezoneForCountry(cc); got != want {
			t.Fatalf("timezoneForCountry(%q) = %q，期望 %q", cc, got, want)
		}
	}
}

func TestSourceIDStable(t *testing.T) {
	a := sourceID("订阅A", "https://example.com/sub")
	b := sourceID("订阅A", "https://example.com/sub")
	c := sourceID("订阅B", "https://example.com/sub")
	if a != b {
		t.Fatal("同一订阅应得到稳定 ID")
	}
	if a == c {
		t.Fatal("不同订阅应有不同 ID")
	}
	if len(a) != 12 {
		t.Fatalf("ID 长度应为 12，实际 %q", a)
	}
}
