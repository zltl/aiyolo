package console

import (
	"net/http"
	"strings"
)

const (
	consoleLocaleZH = "zh-CN"
)

type navItemView struct {
	Key    string
	Label  string
	Href   string
	Target string
	Rel    string
	Icon   string
	Tone   string
}

func consoleText(locale, zh, en string) string {
	return zh
}

func resolveConsoleLocale(r *http.Request) string {
	return consoleLocaleZH
}

func sanitizeConsoleNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || strings.HasPrefix(next, "//") || !strings.HasPrefix(next, "/console") {
		return "/console/"
	}
	return next
}

func consoleNavItems(locale string) []navItemView {
	return []navItemView{
		{Key: "Dashboard", Label: consoleText(locale, "总览", "Dashboard"), Href: "/console/", Icon: "dashboard", Tone: "tone-clay"},
		{Key: "Chat", Label: consoleText(locale, "对话", "Chat"), Href: "/console/chat", Target: "_blank", Rel: "noopener noreferrer", Icon: "chat", Tone: "tone-sea"},
		{Key: "Usage", Label: consoleText(locale, "用量", "Usage"), Href: "/console/usage", Icon: "usage", Tone: "tone-sand"},
		{Key: "API Keys", Label: consoleText(locale, "API 密钥", "API Keys"), Href: "/console/api-keys", Icon: "keys", Tone: "tone-clay"},
		{Key: "Providers", Label: consoleText(locale, "供应商", "Providers"), Href: "/console/providers", Icon: "provider", Tone: "tone-forest"},
		{Key: "Models", Label: consoleText(locale, "模型路由", "Models"), Href: "/console/models", Icon: "models", Tone: "tone-sea"},
		{Key: "Proxies", Label: consoleText(locale, "代理", "Proxies"), Href: "/console/proxies", Icon: "proxies", Tone: "tone-ink"},
		{Key: "Workers", Label: consoleText(locale, "Workers", "Workers"), Href: "/console/workers", Icon: "workers", Tone: "tone-forest"},
		{Key: "Settings", Label: consoleText(locale, "设置", "Settings"), Href: "/console/settings", Icon: "settings", Tone: "tone-sand"},
	}
}

func pageTitleLocalized(locale, title string) string {
	switch title {
	case "Dashboard":
		return consoleText(locale, "总览", "Dashboard")
	case "Chat":
		return consoleText(locale, "对话", "Chat")
	case "Usage":
		return consoleText(locale, "用量", "Usage")
	case "API Keys":
		return consoleText(locale, "API 密钥", "API Keys")
	case "Providers":
		return consoleText(locale, "供应商", "Providers")
	case "Models":
		return consoleText(locale, "模型路由", "Models")
	case "Proxies":
		return consoleText(locale, "代理", "Proxies")
	case "Workers":
		return consoleText(locale, "Workers", "Workers")
	case "Settings":
		return consoleText(locale, "设置", "Settings")
	case "Login":
		return consoleText(locale, "登录", "Login")
	default:
		return title
	}
}

func pageEyebrowLocalized(locale, title string) string {
	switch title {
	case "Dashboard":
		return consoleText(locale, "控制中枢", "Control Center")
	case "Chat":
		return consoleText(locale, "对话工作台", "Conversation Workbench")
	case "Usage":
		return consoleText(locale, "用量与费用", "Usage And Spend")
	case "API Keys":
		return consoleText(locale, "凭证边界", "Credential Surface")
	case "Providers":
		return consoleText(locale, "上游渠道", "Upstream Channels")
	case "Models":
		return consoleText(locale, "模型映射", "Route Map")
	case "Proxies":
		return consoleText(locale, "网络路径", "Network Paths")
	case "Workers":
		return consoleText(locale, "云端执行面", "Cloud Execution")
	case "Settings":
		return consoleText(locale, "访问策略", "Access Policy")
	case "Login":
		return consoleText(locale, "登录", "Sign In")
	default:
		return "AIYolo 控制台"
	}
}

func pageDescriptionLocalized(locale, title string) string {
	switch title {
	case "Dashboard":
		return consoleText(locale, "把请求、错误、费用和热点路由压缩进一块更易读的运营总览。", "Compress requests, errors, spend, and hot routes into a faster operational overview.")
	case "Chat":
		return consoleText(locale, "直接在独立对话工作区里验证公共模型的路由、代理与上游响应。", "Use the dedicated chat workspace to validate routing, proxies, and upstream behavior.")
	case "Usage":
		return consoleText(locale, "在同一页里查看用量账本、近 30 天费用和消费热点，不再拆成两张页面。", "Review the usage ledger, 30-day spend, and cost hotspots in one place instead of two pages.")
	case "API Keys":
		return consoleText(locale, "创建并收紧调用方凭证，把预算、协议和模型边界留在网关内。", "Issue client credentials while keeping budget, protocol, and model boundaries inside the gateway.")
	case "Providers":
		return consoleText(locale, "管理真实上游渠道、一键导入模型和运行时权重。", "Manage upstream channels, one-click model sync, and runtime weights.")
	case "Models":
		return consoleText(locale, "把稳定的对外模型名映射到真实提供方与代理路径。", "Map stable public model names to real providers and proxy paths.")
	case "Proxies":
		return consoleText(locale, "把 direct、HTTP、SOCKS5 路径做成可观察资源，并统一在控制台维护。", "Keep direct, HTTP, and SOCKS5 paths observable and managed from the console.")
	case "Workers":
		return consoleText(locale, "登记 SSH 密钥和 Worker 主机，为后续云端 Agent、终端与文件工作区铺好基础。", "Register SSH keys and worker hosts as the control-plane foundation for future cloud agents, terminals, and workspaces.")
	case "Settings":
		return consoleText(locale, "把登录方式和观察到的后台身份收在一个页面。", "Keep login methods, language preference, and observed console identities on one page.")
	case "Login":
		return consoleText(locale, "进入 AIYolo 控制台，管理网关的路由、凭证与预算。", "Enter the AIYolo web console to manage routing, credentials, and budgets.")
	default:
		return ""
	}
}

func (handler *Handler) decoratePageData(r *http.Request, data map[string]any) map[string]any {
	if data == nil {
		data = make(map[string]any)
	}
	locale := resolveConsoleLocale(r)
	currentPath := "/console/"
	if r != nil {
		if strings.TrimSpace(r.URL.Path) != "" {
			currentPath = r.URL.Path
		}
	}
	data["Locale"] = locale
	data["CurrentPath"] = currentPath
	data["NavItems"] = consoleNavItems(locale)
	if title, ok := data["Title"].(string); ok && strings.TrimSpace(title) != "" {
		data["DisplayTitle"] = pageTitleLocalized(locale, title)
		data["PageEyebrow"] = pageEyebrowLocalized(locale, title)
		data["PageDescription"] = pageDescriptionLocalized(locale, title)
	}
	return data
}

func (handler *Handler) requestText(r *http.Request, zh, en string) string {
	return consoleText(resolveConsoleLocale(r), zh, en)
}
