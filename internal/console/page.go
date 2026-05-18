package console

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	consoleLocaleCookieName = "aiyolo_console_locale"
	consoleLocaleZH         = "zh-CN"
	consoleLocaleEN         = "en"
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

type localeOptionView struct {
	Code   string
	Label  string
	Href   string
	Active bool
}

func consoleText(locale, zh, en string) string {
	if normalizeConsoleLocale(locale) == consoleLocaleEN {
		return en
	}
	return zh
}

func normalizeConsoleLocale(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en", "en-us", "en-gb":
		return consoleLocaleEN
	case "zh", "zh-cn", "zh-hans", "zh-sg", "zh-hk", "zh-tw":
		return consoleLocaleZH
	default:
		return ""
	}
}

func resolveConsoleLocale(r *http.Request) string {
	if r != nil {
		if cookie, err := r.Cookie(consoleLocaleCookieName); err == nil {
			if locale := normalizeConsoleLocale(cookie.Value); locale != "" {
				return locale
			}
		}
		header := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language")))
		switch {
		case strings.Contains(header, "zh"):
			return consoleLocaleZH
		case strings.Contains(header, "en"):
			return consoleLocaleEN
		}
	}
	return consoleLocaleEN
}

func sanitizeConsoleNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || strings.HasPrefix(next, "//") || !strings.HasPrefix(next, "/console") {
		return "/console/"
	}
	return next
}

func localeSwitches(currentURI, locale string) []localeOptionView {
	currentURI = sanitizeConsoleNext(currentURI)
	return []localeOptionView{
		{Code: consoleLocaleZH, Label: "中", Href: "/console/locale?lang=" + url.QueryEscape(consoleLocaleZH) + "&next=" + url.QueryEscape(currentURI), Active: locale == consoleLocaleZH},
		{Code: consoleLocaleEN, Label: "EN", Href: "/console/locale?lang=" + url.QueryEscape(consoleLocaleEN) + "&next=" + url.QueryEscape(currentURI), Active: locale == consoleLocaleEN},
	}
}

func consoleNavItems(locale string) []navItemView {
	return []navItemView{
		{Key: "Dashboard", Label: consoleText(locale, "总览", "Dashboard"), Href: "/console/", Icon: "dashboard", Tone: "tone-clay"},
		{Key: "Codex", Label: consoleText(locale, "Codex", "Codex"), Href: "/console/codex", Icon: "codex", Tone: "tone-forest"},
		{Key: "Chat", Label: consoleText(locale, "Chat", "Chat"), Href: "/console/chat", Target: "_blank", Rel: "noopener noreferrer", Icon: "chat", Tone: "tone-sea"},
		{Key: "Usage", Label: consoleText(locale, "用量", "Usage"), Href: "/console/usage", Icon: "usage", Tone: "tone-sand"},
		{Key: "Audit", Label: consoleText(locale, "审计", "Audit"), Href: "/console/audit", Icon: "audit", Tone: "tone-ink"},
		{Key: "API Keys", Label: consoleText(locale, "API 密钥", "API Keys"), Href: "/console/api-keys", Icon: "keys", Tone: "tone-clay"},
		{Key: "Providers", Label: consoleText(locale, "供应商", "Providers"), Href: "/console/providers", Icon: "provider", Tone: "tone-forest"},
		{Key: "Models", Label: consoleText(locale, "模型路由", "Models"), Href: "/console/models", Icon: "models", Tone: "tone-sea"},
		{Key: "Proxies", Label: consoleText(locale, "代理", "Proxies"), Href: "/console/proxies", Icon: "proxies", Tone: "tone-ink"},
		{Key: "Settings", Label: consoleText(locale, "设置", "Settings"), Href: "/console/settings", Icon: "settings", Tone: "tone-sand"},
	}
}

func pageTitleLocalized(locale, title string) string {
	switch title {
	case "Dashboard":
		return consoleText(locale, "总览", "Dashboard")
	case "Codex":
		return "Codex"
	case "Chat":
		return consoleText(locale, "对话", "Chat")
	case "Usage":
		return consoleText(locale, "用量", "Usage")
	case "Audit":
		return consoleText(locale, "审计", "Audit")
	case "API Keys":
		return consoleText(locale, "API 密钥", "API Keys")
	case "Providers":
		return consoleText(locale, "供应商", "Providers")
	case "Models":
		return consoleText(locale, "模型路由", "Models")
	case "Proxies":
		return consoleText(locale, "代理", "Proxies")
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
	case "Codex":
		return consoleText(locale, "Windows 安装器", "Windows Installer")
	case "Chat":
		return consoleText(locale, "对话工作台", "Conversation Workbench")
	case "Usage":
		return consoleText(locale, "用量与费用", "Usage And Spend")
	case "Audit":
		return consoleText(locale, "审计时间线", "Audit Trail")
	case "API Keys":
		return consoleText(locale, "凭证边界", "Credential Surface")
	case "Providers":
		return consoleText(locale, "上游渠道", "Upstream Channels")
	case "Models":
		return consoleText(locale, "模型映射", "Route Map")
	case "Proxies":
		return consoleText(locale, "网络路径", "Network Paths")
	case "Settings":
		return consoleText(locale, "访问策略", "Access Policy")
	case "Login":
		return consoleText(locale, "登录", "Sign In")
	default:
		return "AIYolo Console"
	}
}

func pageDescriptionLocalized(locale, title string) string {
	switch title {
	case "Dashboard":
		return consoleText(locale, "把请求、错误、费用和热点路由压缩进一块更易读的运营总览。", "Compress requests, errors, spend, and hot routes into a faster operational overview.")
	case "Codex":
		return consoleText(locale, "为 Windows 用户生成一次性 AIYolo Codex 安装命令，并自动配置受限 API Key。", "Generate one-time Windows AIYolo Codex install commands with scoped API keys configured automatically.")
	case "Chat":
		return consoleText(locale, "直接在独立 chat 工作区里验证 public model 的路由、代理与上游响应。", "Use the dedicated chat workspace to validate routing, proxies, and upstream behavior.")
	case "Usage":
		return consoleText(locale, "在同一页里查看用量账本、近 30 天费用和消费热点，不再拆成两张页面。", "Review the usage ledger, 30-day spend, and cost hotspots in one place instead of two pages.")
	case "Audit":
		return consoleText(locale, "沿着请求、认证和失败链路回看问题发生在哪一跳。", "Trace requests, auth, and failures to see exactly where an issue happened.")
	case "API Keys":
		return consoleText(locale, "创建并收紧调用方凭证，把预算、协议和模型边界留在网关内。", "Issue client credentials while keeping budget, protocol, and model boundaries inside the gateway.")
	case "Providers":
		return consoleText(locale, "管理真实上游渠道、一键导入模型和运行时权重。", "Manage upstream channels, one-click model sync, and runtime weights.")
	case "Models":
		return consoleText(locale, "把稳定的对外模型名映射到真实 provider 与代理路径。", "Map stable public model names to real providers and proxy paths.")
	case "Proxies":
		return consoleText(locale, "把 direct、HTTP、SOCKS5 路径做成可观察资源，并统一在控制台维护。", "Keep direct, HTTP, and SOCKS5 paths observable and managed from the console.")
	case "Settings":
		return consoleText(locale, "把登录方式、语言偏好和观察到的后台身份收在一个页面。", "Keep login methods, language preference, and observed console identities on one page.")
	case "Login":
		return consoleText(locale, "进入 AIYolo web console，管理网关的路由、凭证、预算与审计。", "Enter the AIYolo web console to manage routing, credentials, budgets, and audit.")
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
	currentURI := currentPath
	if r != nil {
		if strings.TrimSpace(r.URL.Path) != "" {
			currentPath = r.URL.Path
		}
		if strings.TrimSpace(r.URL.RequestURI()) != "" {
			currentURI = r.URL.RequestURI()
		}
	}
	data["Locale"] = locale
	data["CurrentPath"] = currentPath
	data["CurrentURI"] = currentURI
	data["NavItems"] = consoleNavItems(locale)
	data["LocaleOptions"] = localeSwitches(currentURI, locale)
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
