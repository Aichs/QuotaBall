package quotaball_test

import (
	"os"
	"strings"
	"testing"
)

func TestREADMEIncludesCurrentProductAndProviderScope(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)

	for _, want := range []string{
		"# QuotaBall",
		"Krill AI",
		"NewAPI",
		"LinuxDo",
		"周额度",
		"月总额度",
		"当前余额",
		"历史消耗",
		"请求次数",
		"cmd\\quotaball",
		"dist\\QuotaBall.exe",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README must document %q", want)
		}
	}
}

func TestREADMEAppendsFriendsLinksSection(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)

	for _, want := range []string{
		"## 🤝 Friends / Links",
		"https://linux.do",
		"LINUX.DO-Community-000000",
		"真诚、友善、团结、专业，共建你我引以为荣之社区。",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README Friends / Links section must include %q", want)
		}
	}
	if strings.LastIndex(readme, "## 🤝 Friends / Links") < strings.LastIndex(readme, "## ") {
		t.Fatalf("Friends / Links section should be the final README section")
	}
}
