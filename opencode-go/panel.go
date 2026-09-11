package main

import (
	"html/template"
	"strings"
	"time"
)

// panelData is the template model for the management dashboard.
type panelData struct {
	GeneratedAt         time.Time
	Accounts            []AccountResult
	View                string
	PluginID            string
	BootstrapManagement bool
}

var panelHelpers = template.FuncMap{
	"windows":       displayWindows,
	"remainingText": remainingText,
	"resetText":     resetText,
	"statusClass":   statusClass,
	"mask":          func(value string) string { return maskCredential(value) },
	"percent":       func(value float64) string { return formatPercent(value) },
	"sourceLabel":   sourceLabel,
}

var panelTemplate = template.Must(template.New("panel").Funcs(panelHelpers).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenCode Go</title><style>
#ocgo-panel{color-scheme:dark;--oc-bg:#0d1017;--oc-panel:#151a24;--oc-control:#10141c;--oc-line:#2b3444;--oc-text:#e6ebf2;--oc-muted:#8f9bb0;--oc-accent:#6aa8ff;--oc-accent-strong:#2a4a7a;--oc-ok:#4cd08a;--oc-warn:#f1b46c;--oc-danger:#f08383}
*{box-sizing:border-box}body{margin:0;background:var(--oc-bg);color:var(--oc-text);font:14px/1.5 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:inherit;text-decoration:none}
.app{display:grid;grid-template-columns:172px minmax(0,1fr);min-height:100vh}
.nav{padding:22px 12px;background:#0a0d13;border-right:1px solid var(--oc-line)}
.logo{padding:0 8px 22px;font-weight:800;letter-spacing:-.03em;font-size:15px}
.nav-group{display:grid;gap:6px}
.nav-item{display:block;padding:11px;border-radius:9px;color:var(--oc-muted);font-size:12px;font-weight:700}
.nav-item.active{background:#1c2637;color:var(--oc-text)}
.nav-item small{display:block;margin-top:3px;font-size:10px;font-weight:500;opacity:.6}
.main{min-width:0;padding:34px clamp(18px,4vw,42px) 56px}
.header{display:flex;justify-content:space-between;align-items:flex-start;gap:18px;padding-bottom:18px;border-bottom:1px solid var(--oc-line);flex-wrap:wrap}
h1{margin:0;font-size:24px;letter-spacing:-.03em}h2{margin:0;font-size:17px}
p{margin:5px 0 0;color:var(--oc-muted);font-size:12px}
.meta{display:flex;gap:14px;flex-wrap:wrap;margin-top:12px;color:var(--oc-muted);font-size:11px}
.action{border:0;border-radius:999px;padding:10px 16px;background:var(--oc-accent);color:#08131f;font:inherit;font-size:12px;font-weight:800;cursor:pointer}
.action.ghost{background:transparent;border:1px solid var(--oc-line);color:var(--oc-text)}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:14px;margin-top:20px}
.card{min-width:0;padding:16px;border:1px solid var(--oc-line);border-radius:14px;background:var(--oc-panel)}
.card-head{display:flex;justify-content:space-between;gap:10px;align-items:flex-start}
.card-title{font-weight:800;font-size:14px;word-break:break-word}
.card-sub{margin-top:4px;color:var(--oc-muted);font-size:11px}
.card-actions{display:flex;gap:6px;margin-top:14px}
.refresh{border:1px solid var(--oc-line);border-radius:8px;padding:6px 9px;background:transparent;color:var(--oc-text);font:inherit;font-size:11px;cursor:pointer}
.refresh:hover{background:#1c2637}
.window{margin-top:15px}
.window-head{display:flex;justify-content:space-between;gap:8px;color:var(--oc-muted);font-size:11px}
.remaining{color:var(--oc-text);font-variant-numeric:tabular-nums;font-weight:700}
.bar{height:7px;margin-top:6px;border-radius:4px;background:#232c3b;overflow:hidden}
.fill{height:100%;border-radius:inherit;background:var(--oc-ok)}
.fill.warn{background:var(--oc-warn)}.fill.danger{background:var(--oc-danger)}
.reset{margin-top:4px;color:var(--oc-muted);font-size:10px}
.error{margin-top:14px;padding:9px 10px;border:1px solid #5a3030;border-radius:9px;color:var(--oc-warn);background:#1d1417;font-size:11px;word-break:break-word}
.status{display:inline-flex;align-items:center;gap:5px;font-size:11px;color:var(--oc-ok)}
.status:before{content:"";width:6px;height:6px;border-radius:50%;background:currentColor}
.status.warn{color:var(--oc-warn)}.status.danger{color:var(--oc-danger)}
.mask{margin-top:12px;border:1px solid var(--oc-line);border-radius:9px;padding:9px;color:var(--oc-muted);background:var(--oc-control);font:600 11px ui-monospace,SFMono-Regular,Menlo,monospace;word-break:break-all}
.empty{margin-top:20px;padding:34px;border:1px dashed var(--oc-line);border-radius:14px;text-align:center;color:var(--oc-muted)}
.table{width:100%;margin-top:20px;border-collapse:collapse;font-size:12px}
.table th,.table td{padding:11px 12px;border-bottom:1px solid var(--oc-line);text-align:left;vertical-align:top}
.table th{color:var(--oc-muted);font-size:11px;font-weight:700}
.table td.mono{font:600 11px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--oc-muted)}
.danger-btn{border:1px solid #5a3030;border-radius:8px;padding:5px 9px;background:transparent;color:var(--oc-danger);font:inherit;font-size:11px;cursor:pointer}
.danger-btn:hover{background:#291419}
.oc-overlay{position:fixed;inset:0;display:grid;place-items:center;padding:22px;background:rgba(4,7,12,.76)}
.oc-overlay[hidden]{display:none}
.oc-modal{width:min(540px,100%);border:1px solid var(--oc-line);border-radius:16px;background:var(--oc-panel);box-shadow:0 24px 70px rgba(0,0,0,.5)}
.oc-modal-head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px;padding:18px;border-bottom:1px solid var(--oc-line)}
.oc-modal-head h3{margin:0;font-size:16px}
.oc-close{border:0;background:transparent;color:var(--oc-muted);font-size:20px;cursor:pointer;line-height:1}
.oc-form{display:grid;gap:13px;padding:18px}
.oc-field{display:grid;gap:6px}
.oc-field label{font-size:11px;font-weight:700}
.oc-field small{color:var(--oc-muted);font-size:10px}
.oc-input{width:100%;border:1px solid var(--oc-line);border-radius:9px;padding:10px;color:var(--oc-text);background:var(--oc-control);font:inherit;font-size:12px}
.oc-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:2px}
.oc-note{color:var(--oc-muted);font-size:10px}
.oc-error{border-radius:9px;padding:9px 10px;background:#1d1417;border:1px solid #5a3030;color:var(--oc-warn);font-size:11px}
.oc-error[hidden]{display:none}
@media(max-width:720px){.app{grid-template-columns:1fr}.nav{display:flex;gap:8px;align-items:center;border-right:0;border-bottom:1px solid var(--oc-line);padding:12px}.logo{padding:0 10px 0 0}.nav-group{display:flex}.nav-item{white-space:nowrap}}
html.cpamp-plugin-host,html[data-cpamp-plugin-host],html.cpamp-plugin-host body,body#ocgo-panel,#ocgo-panel,#ocgo-panel .app,#ocgo-panel .main{background:#0d1017!important;color:#e6ebf2!important;color-scheme:dark!important}
</style></head>
<body id="ocgo-panel"><div class="app">
<nav class="nav"><div class="logo">OpenCode Go</div><div class="nav-group">
<a class="nav-item{{if eq .View "overview"}} active{{end}}" href="?view=overview">订阅用量<small>滚动 / 每周 / 每月</small></a>
<a class="nav-item{{if eq .View "accounts"}} active{{end}}" href="?view=accounts">账号管理<small>保存与删除 Key</small></a>
</div></nav>
<main class="main">
<div class="header"><div><h1>OpenCode Go 订阅用量</h1>
<p>滚动 5 小时 / 每周 / 每月额度来自 opencode.ai 官方用量接口。</p>
<div class="meta"><span>插件 {{.PluginID}}</span><span>更新时间 {{.GeneratedAt.Format "2006-01-02 15:04:05"}} UTC</span><span>账号 {{len .Accounts}}</span></div></div>
<div class="card-actions">
{{if eq .View "overview"}}<button class="action ghost" type="button" data-oc-action="refresh-all">刷新全部</button>{{end}}
<button class="action" type="button" data-oc-action="open-add">添加账号</button></div></div>
{{if eq .View "accounts"}}
{{if .Accounts}}
<table class="table"><thead><tr><th>账号</th><th>来源</th><th>凭据</th><th>状态</th><th></th></tr></thead><tbody>
{{range .Accounts}}<tr>
<td><strong>{{.Label}}</strong><div class="card-sub">{{.ID}}</div></td>
<td class="mono">{{sourceLabel .Source}}</td>
<td class="mono">{{mask .CredentialMask}}</td>
<td>{{if .Error}}<span class="status danger">不可用</span>{{else}}<span class="status">可用</span>{{end}}</td>
<td><button class="danger-btn" type="button" data-oc-action="delete-account" data-oc-id="{{.ID}}">删除</button></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">暂无账号。点击「添加账号」保存 OpenCode Go API Key。</div>{{end}}
{{else}}
{{if .Accounts}}<div class="grid">
{{range .Accounts}}<div class="card">
<div class="card-head"><div><div class="card-title">{{.Label}}</div>
<div class="card-sub">{{if .Plan}}{{.Plan}} · {{end}}{{sourceLabel .Source}}</div></div>
{{if .Error}}<span class="status danger">异常</span>{{else}}<span class="status">正常</span>{{end}}</div>
{{if .Error}}<div class="error">{{.Error}}</div>{{else}}
{{range windows .Usage}}<div class="window">
<div class="window-head"><span>{{.Name}}</span><span class="remaining">{{remainingText .}}</span></div>
<div class="bar"><div class="fill {{statusClass .}}" style="width:{{percent .RemainingPercent}}"></div></div>
<div class="reset">{{resetText .}}</div></div>{{end}}
{{end}}
<div class="mask">API Key {{mask .CredentialMask}}</div>
<div class="card-actions"><button class="refresh" type="button" data-oc-action="refresh-one" data-oc-id="{{.ID}}">刷新</button></div>
</div>{{end}}
</div>{{else}}<div class="empty">暂无账号。点击「添加账号」保存 OpenCode Go API Key。</div>{{end}}
{{end}}
</main></div>
<div class="oc-overlay" id="oc-add-overlay" hidden><div class="oc-modal">
<div class="oc-modal-head"><div><h3>添加 OpenCode Go 账号</h3><p>API Key 只保存在 CPA 认证目录下的私有文件，面板只显示遮罩。</p></div>
<button class="oc-close" type="button" data-oc-action="close-add">&times;</button></div>
<form class="oc-form" id="oc-add-form">
<div class="oc-error" id="oc-add-error" hidden></div>
<div class="oc-field"><label for="oc-label">账号名称</label>
<input class="oc-input" id="oc-label" name="label" autocomplete="off" placeholder="例如 OpenCode Go 主账号" required>
<small>用于显示与生成账号 ID。</small></div>
<div class="oc-field"><label for="oc-credential">API Key</label>
<input class="oc-input" id="oc-credential" name="credential" type="password" autocomplete="off" placeholder="sk-..." required>
<small>保存前会调用官方用量接口校验一次。</small></div>
<div class="oc-field"><label for="oc-endpoint">端点覆盖（可选）</label>
<input class="oc-input" id="oc-endpoint" name="endpoint" autocomplete="off" placeholder="https://opencode.ai/zen/go/v1">
<small>仅允许 https://opencode.ai/zen/go/v1。</small></div>
<div class="oc-actions"><button class="action ghost" type="button" data-oc-action="close-add">取消</button>
<button class="action" type="submit">保存并校验</button></div></form></div></div>
<script>(function(){
var root=document.getElementById('ocgo-panel');if(!root)return;
var pluginId={{.PluginID}};
var base='/v0/management/plugins/'+pluginId+'/accounts';
var overlay=document.getElementById('oc-add-overlay');
var form=document.getElementById('oc-add-form');
var errorBox=document.getElementById('oc-add-error');
function showError(message){if(!errorBox)return;errorBox.textContent=message;errorBox.hidden=false}
function clearError(){if(!errorBox)return;errorBox.textContent='';errorBox.hidden=true}
root.addEventListener('click',function(event){
var target=event.target.closest('[data-oc-action]');if(!target)return;
var action=target.getAttribute('data-oc-action');var id=target.getAttribute('data-oc-id')||'';
if(action==='open-add'){clearError();overlay.hidden=false;var field=document.getElementById('oc-label');if(field)field.focus();}
else if(action==='close-add'){overlay.hidden=true;}
else if(action==='refresh-all'){var url=new URL(location.href);url.searchParams.set('refresh','all');url.searchParams.set('view','overview');location.href=url.toString();}
else if(action==='refresh-one'&&id){var u=new URL(location.href);u.searchParams.set('refresh',id);location.href=u.toString();}
else if(action==='delete-account'&&id){if(!confirm('删除账号 '+id+' ？'))return;
fetch(base+'?id='+encodeURIComponent(id),{method:'DELETE',credentials:'include'}).then(function(response){
if(response.ok){location.reload();return;}return response.json().then(function(data){alert((data&&data.error)||'删除失败');});});}
});
if(form){form.addEventListener('submit',function(event){event.preventDefault();clearError();
var payload={label:(document.getElementById('oc-label')||{}).value||'',credential:(document.getElementById('oc-credential')||{}).value||'',endpoint:(document.getElementById('oc-endpoint')||{}).value||''};
fetch(base,{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)}).then(function(response){
return response.json().catch(function(){return {};}).then(function(data){
if(response.ok){location.reload();return;}
showError((data&&data.error)||('保存失败（'+response.status+'）'));});});});}
})();</script>
{{if .BootstrapManagement}}<script>(function(){try{var stores=[];try{stores.push(window.localStorage)}catch(e){}try{stores.push(window.sessionStorage)}catch(e){}if(window.parent&&window.parent!==window){try{stores.push(window.parent.localStorage)}catch(e){}try{stores.push(window.parent.sessionStorage)}catch(e){}}var authed=stores.some(function(store){try{return String(store&&store.getItem('cli-proxy-auth')||'').trim()!==''}catch(e){return false}});if(authed){var link=document.querySelector('[data-management-view="overview"]');if(link)link.click();}}catch(e){}})();</script>{{end}}
</body></html>`))

// displayWindows returns the usage windows in display order.
func displayWindows(usage *UsageResult) []UsageWindow {
	if usage == nil {
		return nil
	}
	if len(usage.Windows) > 0 {
		return usage.Windows
	}
	windows := make([]UsageWindow, 0, 3)
	for _, candidate := range []UsageWindow{usage.Rolling, usage.Weekly, usage.Monthly} {
		if candidate.Name != "" {
			windows = append(windows, candidate)
		}
	}
	return windows
}

func windowLabel(name string) string {
	switch name {
	case "five_hour":
		return "滚动 5 小时"
	case "weekly":
		return "每周"
	case "monthly":
		return "每月"
	default:
		return name
	}
}

func remainingText(window UsageWindow) string {
	if strings.EqualFold(window.Status, "rate-limited") {
		return "已达上限"
	}
	return formatPercent(window.RemainingPercent) + " 剩余"
}

func resetText(window UsageWindow) string {
	label := windowLabel(window.Name)
	if strings.EqualFold(window.Status, "rate-limited") {
		label += " · 已达上限"
	}
	if window.ResetAt == nil {
		return label
	}
	return label + " · 重置 " + window.ResetAt.UTC().Format("2006-01-02 15:04 UTC")
}

func statusClass(window UsageWindow) string {
	if strings.EqualFold(window.Status, "rate-limited") || window.RemainingPercent <= 0 {
		return "danger"
	}
	if window.RemainingPercent <= 20 {
		return "warn"
	}
	return ""
}

func sourceLabel(source AuthSource) string {
	switch source {
	case SourceConfig:
		return "配置文件"
	case SourcePanel:
		return "面板保存"
	case SourceCPA:
		return "CPA 认证"
	default:
		if strings.TrimSpace(string(source)) == "" {
			return "未知"
		}
		return string(source)
	}
}

// RenderPanelView renders the authenticated management dashboard.
func RenderPanelView(accounts []AccountResult, generatedAt time.Time, view string) string {
	return renderPanel(accounts, generatedAt, view, false)
}

// RenderResourcePanel renders the cache-free shell used for browser resources.
func RenderResourcePanel(generatedAt time.Time) string {
	return renderPanel(nil, generatedAt, "overview", true)
}

func renderPanel(accounts []AccountResult, generatedAt time.Time, view string, bootstrap bool) string {
	if view != "accounts" {
		view = "overview"
	}
	var output strings.Builder
	data := panelData{GeneratedAt: generatedAt, Accounts: accounts, View: view, PluginID: pluginID, BootstrapManagement: bootstrap}
	if err := panelTemplate.Execute(&output, data); err != nil {
		return "<!doctype html><title>OpenCode Go</title><p>面板渲染失败：" + template.HTMLEscapeString(err.Error()) + "</p>"
	}
	return output.String()
}
