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
	return ""
}

func pageDescriptionLocalized(locale, title string) string {
	return ""
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
