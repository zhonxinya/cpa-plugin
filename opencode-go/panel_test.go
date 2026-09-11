package main

import (
	"strings"
	"testing"
	"time"
)

var panelTestTime = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

func sampleResults() []AccountResult {
	return []AccountResult{
		{
			ID:             "opencode-go-main",
			Provider:       providerKey,
			Plan:           "go",
			Label:          "主账号",
			Source:         SourcePanel,
			CredentialMask: maskCredential("sk-secret"),
			FetchedAt:      panelTestTime,
			Usage: &UsageResult{
				Level:   "OpenCode Go",
				Rolling: UsageWindow{Name: "five_hour", Status: "ok", RemainingPercent: 98, UsedPercent: 2},
				Weekly:  UsageWindow{Name: "weekly", Status: "ok", RemainingPercent: 94, UsedPercent: 6},
				Monthly: UsageWindow{Name: "monthly", Status: "rate-limited", RemainingPercent: 0, UsedPercent: 100},
				Windows: []UsageWindow{
					{Name: "five_hour", Status: "ok", RemainingPercent: 98},
					{Name: "weekly", Status: "ok", RemainingPercent: 94},
					{Name: "monthly", Status: "rate-limited", RemainingPercent: 0},
				},
			},
		},
	}
}

func TestRenderPanelOverview(t *testing.T) {
	html := RenderPanelView(sampleResults(), panelTestTime, "overview")
	for _, want := range []string{"主账号", "滚动 5 小时", "每周", "每月", "已达上限", "98.0% 剩余"} {
		if !strings.Contains(html, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	if strings.Contains(html, "sk-secret") {
		t.Error("overview leaked the credential")
	}
}

func TestRenderPanelEscapesAccountLabels(t *testing.T) {
	results := []AccountResult{{ID: "a", Label: `<script>alert(1)</script>`, Source: SourcePanel, FetchedAt: panelTestTime, Error: "boom"}}
	html := RenderPanelView(results, panelTestTime, "overview")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("panel did not escape the account label")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("escaped label missing from panel")
	}
}

func TestRenderPanelEmptyState(t *testing.T) {
	html := RenderPanelView(nil, panelTestTime, "accounts")
	if !strings.Contains(html, "暂无账号") {
		t.Error("accounts view missing the empty state")
	}
}

func TestRenderPanelAccountsView(t *testing.T) {
	html := RenderPanelView(sampleResults(), panelTestTime, "accounts")
	if !strings.Contains(html, "<table") || !strings.Contains(html, "data-oc-action=\"delete-account\"") {
		t.Error("accounts view missing the management table")
	}
	if !strings.Contains(html, "配置文件") && !strings.Contains(html, "面板保存") {
		t.Error("accounts view missing the source label")
	}
}

func TestRenderResourcePanelHidesAccountData(t *testing.T) {
	html := RenderResourcePanel(panelTestTime)
	if !strings.Contains(html, "OpenCode Go") {
		t.Error("resource panel missing the shell")
	}
	// The unauthenticated resource route must not render account-bound elements.
	for _, forbidden := range []string{"data-oc-id=", "API Key •"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("resource panel leaked %q", forbidden)
		}
	}
	if !strings.Contains(html, "cli-proxy-auth") {
		t.Error("resource panel missing the management bootstrap script")
	}
}

func TestWindowHelpers(t *testing.T) {
	if got := windowLabel("five_hour"); got != "滚动 5 小时" {
		t.Errorf("windowLabel(five_hour) = %q", got)
	}
	if got := windowLabel("weekly"); got != "每周" {
		t.Errorf("windowLabel(weekly) = %q", got)
	}
	if got := windowLabel("custom"); got != "custom" {
		t.Errorf("windowLabel(custom) = %q", got)
	}
	if got := remainingText(UsageWindow{Status: "rate-limited"}); got != "已达上限" {
		t.Errorf("remainingText(rate-limited) = %q", got)
	}
	if got := remainingText(UsageWindow{RemainingPercent: 42}); got != "42.0% 剩余" {
		t.Errorf("remainingText(42) = %q", got)
	}
	reset := panelTestTime
	if got := resetText(UsageWindow{Name: "weekly", ResetAt: &reset}); !strings.Contains(got, "重置") {
		t.Errorf("resetText = %q, want reset marker", got)
	}
	if got := resetText(UsageWindow{Name: "weekly"}); got != "每周" {
		t.Errorf("resetText(no time) = %q", got)
	}
}

func TestStatusClass(t *testing.T) {
	cases := []struct {
		window UsageWindow
		want   string
	}{
		{UsageWindow{Status: "rate-limited"}, "danger"},
		{UsageWindow{RemainingPercent: 0}, "danger"},
		{UsageWindow{RemainingPercent: 15}, "warn"},
		{UsageWindow{RemainingPercent: 80}, ""},
	}
	for _, testCase := range cases {
		if got := statusClass(testCase.window); got != testCase.want {
			t.Errorf("statusClass(%#v) = %q, want %q", testCase.window, got, testCase.want)
		}
	}
}

func TestSourceLabel(t *testing.T) {
	if got := sourceLabel(SourceConfig); got != "配置文件" {
		t.Errorf("sourceLabel(SourceConfig) = %q", got)
	}
	if got := sourceLabel(SourceCPA); got != "CPA 认证" {
		t.Errorf("sourceLabel(SourceCPA) = %q", got)
	}
	if got := sourceLabel(""); got != "未知" {
		t.Errorf("sourceLabel(empty) = %q", got)
	}
	if got := sourceLabel("other"); got != "other" {
		t.Errorf("sourceLabel(other) = %q", got)
	}
}

func TestDisplayWindowsFallsBackToNamedFields(t *testing.T) {
	usage := &UsageResult{
		Rolling: UsageWindow{Name: "five_hour"},
		Weekly:  UsageWindow{Name: "weekly"},
	}
	if got := displayWindows(usage); len(got) != 2 {
		t.Errorf("displayWindows = %#v", got)
	}
	if got := displayWindows(nil); got != nil {
		t.Errorf("displayWindows(nil) = %#v", got)
	}
}
