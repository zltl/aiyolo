package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

const (
	consoleSessionCookieName    = "aiyolo_console"
	consoleOAuthStateCookieName = "aiyolo_console_oauth"
	oauthKindOAuth2             = "oauth2"
	oauthKindOIDC               = "oidc"
	tokenStyleForm              = "form"
	tokenStyleJSON              = "json"
	clientAuthParams            = "params"
	clientAuthBasic             = "basic"
	userInfoMethodGet           = "GET"
	userInfoMethodPost          = "POST"
	userInfoTokenBearer         = "bearer"
	userInfoTokenQuery          = "query"
	defaultTokenResponsePath    = "access_token"
)

type loginProviderView struct {
	Name string
	URL  string
}

type authProviderView struct {
	ID                  string
	Name                string
	Kind                string
	Enabled             bool
	ClientID            string
	ClientSecret        string
	ScopesText          string
	AuthURL             string
	TokenURL            string
	TokenStyle          string
	TokenResponsePath   string
	AuthStyle           string
	UserInfoURL         string
	UserInfoMethod      string
	UserInfoTokenStyle  string
	UserInfoSubjectPath string
	UserInfoEmailPath   string
	UserInfoNamePath    string
	UserInfoLoginPath   string
	ExtraEmailURL       string
	IssuerURL           string
	AuthParamsText      string
	TokenParamsText     string
	UserInfoParamsText  string
	CallbackURL         string
	FormPrefix          string
	Ready               bool
}

type oauthIdentity struct {
	Subject string
	Email   string
	Name    string
	Login   string
}

func (handler *Handler) renderLoginPage(w http.ResponseWriter, r *http.Request, errorMessage string) {
	data, err := handler.loginTemplateData(r.Context(), r, errorMessage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, r, "login", data)
}

func (handler *Handler) renderSettingsPage(w http.ResponseWriter, r *http.Request, notice, errorMessage string) {
	data, err := handler.settingsTemplateData(r.Context(), r, notice, errorMessage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, r, "settings", data)
}

func (handler *Handler) loginTemplateData(ctx context.Context, r *http.Request, errorMessage string) (map[string]any, error) {
	settings, err := handler.consoleAuthSettings(ctx)
	if err != nil {
		return nil, err
	}
	providers := make([]loginProviderView, 0)
	for _, provider := range settings.Providers {
		if !providerReady(provider) {
			continue
		}
		providers = append(providers, loginProviderView{Name: provider.Name, URL: "/console/login/" + provider.ID})
	}
	return map[string]any{
		"Title":                "Login",
		"AdminEmail":           handler.cfg.AdminEmail,
		"Error":                errorMessage,
		"LocalPasswordEnabled": settings.LocalPasswordEnabled,
		"Providers":            providers,
		"HasOAuthProviders":    len(providers) > 0,
	}, nil
}

func (handler *Handler) settingsTemplateData(ctx context.Context, r *http.Request, notice, errorMessage string) (map[string]any, error) {
	settings, err := handler.consoleAuthSettings(ctx)
	if err != nil {
		return nil, err
	}
	userDirectory, err := handler.store.UserDirectory(ctx)
	if err != nil {
		return nil, err
	}
	userDirectory.Settings = settings
	providers := make([]authProviderView, 0, len(settings.Providers))
	for _, provider := range settings.Providers {
		providers = append(providers, authProviderView{
			ID:                  provider.ID,
			Name:                provider.Name,
			Kind:                providerKind(provider),
			Enabled:             provider.Enabled,
			ClientID:            provider.ClientID,
			ClientSecret:        provider.ClientSecret,
			ScopesText:          strings.Join(provider.Scopes, ","),
			AuthURL:             provider.AuthURL,
			TokenURL:            provider.TokenURL,
			TokenStyle:          tokenStyle(provider),
			TokenResponsePath:   firstNonEmpty(provider.TokenResponsePath, defaultTokenResponsePath),
			AuthStyle:           providerAuthStyle(provider),
			UserInfoURL:         provider.UserInfoURL,
			UserInfoMethod:      userInfoMethod(provider),
			UserInfoTokenStyle:  firstNonEmpty(provider.UserInfoTokenStyle, userInfoTokenBearer),
			UserInfoSubjectPath: provider.UserInfoSubjectPath,
			UserInfoEmailPath:   provider.UserInfoEmailPath,
			UserInfoNamePath:    provider.UserInfoNamePath,
			UserInfoLoginPath:   provider.UserInfoLoginPath,
			ExtraEmailURL:       provider.ExtraEmailURL,
			IssuerURL:           provider.IssuerURL,
			AuthParamsText:      keyValuesText(provider.AuthParams),
			TokenParamsText:     keyValuesText(provider.TokenParams),
			UserInfoParamsText:  keyValuesText(provider.UserInfoParams),
			CallbackURL:         consoleExternalURL(r, "/console/oauth/"+provider.ID+"/callback"),
			FormPrefix:          "provider_" + provider.ID + "_",
			Ready:               providerReady(provider),
		})
	}
	return map[string]any{
		"Title":                "Settings",
		"Notice":               notice,
		"Error":                errorMessage,
		"LocalPasswordEnabled": settings.LocalPasswordEnabled,
		"AllowedEmailsText":    strings.Join(settings.AllowedEmails, "\n"),
		"AllowedDomainsText":   strings.Join(settings.AllowedDomains, "\n"),
		"AdminEmail":           handler.cfg.AdminEmail,
		"AuthProviders":        providers,
		"UserDirectory":        userDirectory,
	}, nil
}

func (handler *Handler) saveAuthSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	current, err := handler.consoleAuthSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	settings := domain.ConsoleAuthSettings{
		LocalPasswordEnabled: r.FormValue("local_password_enabled") != "",
		AllowedEmails:        splitLinesCSV(r.FormValue("allowed_emails")),
		AllowedDomains:       splitLinesCSV(r.FormValue("allowed_domains")),
		UpdatedAt:            time.Now().UTC(),
	}
	currentProviders := make(map[string]domain.OAuthProviderSettings, len(current.Providers))
	for _, provider := range current.Providers {
		currentProviders[provider.ID] = provider
	}
	for _, currentProvider := range current.Providers {
		prefix := "provider_" + currentProvider.ID + "_"
		provider := currentProvider
		provider.Enabled = r.FormValue(prefix+"enabled") != ""
		provider.Kind = firstNonEmpty(strings.TrimSpace(r.FormValue(prefix+"kind")), providerKind(provider))
		provider.ClientID = strings.TrimSpace(r.FormValue(prefix + "client_id"))
		provider.ClientSecret = strings.TrimSpace(r.FormValue(prefix + "client_secret"))
		if provider.ClientSecret == "" {
			provider.ClientSecret = currentProviders[provider.ID].ClientSecret
		}
		provider.Scopes = splitLinesCSV(r.FormValue(prefix + "scopes"))
		provider.AuthURL = strings.TrimSpace(r.FormValue(prefix + "auth_url"))
		provider.TokenURL = strings.TrimSpace(r.FormValue(prefix + "token_url"))
		provider.UserInfoURL = strings.TrimSpace(r.FormValue(prefix + "userinfo_url"))
		provider.IssuerURL = strings.TrimSpace(r.FormValue(prefix + "issuer_url"))
		provider.TokenStyle = firstNonEmpty(strings.TrimSpace(r.FormValue(prefix+"token_style")), tokenStyle(provider))
		provider.TokenResponsePath = firstNonEmpty(strings.TrimSpace(r.FormValue(prefix+"token_response_path")), defaultTokenResponsePath)
		provider.AuthStyle = firstNonEmpty(strings.TrimSpace(r.FormValue(prefix+"auth_style")), providerAuthStyle(provider))
		provider.UserInfoMethod = firstNonEmpty(strings.ToUpper(strings.TrimSpace(r.FormValue(prefix+"userinfo_method"))), userInfoMethod(provider))
		provider.UserInfoTokenStyle = firstNonEmpty(strings.TrimSpace(r.FormValue(prefix+"userinfo_token_style")), firstNonEmpty(provider.UserInfoTokenStyle, userInfoTokenBearer))
		provider.UserInfoSubjectPath = strings.TrimSpace(r.FormValue(prefix + "userinfo_subject_path"))
		provider.UserInfoEmailPath = strings.TrimSpace(r.FormValue(prefix + "userinfo_email_path"))
		provider.UserInfoNamePath = strings.TrimSpace(r.FormValue(prefix + "userinfo_name_path"))
		provider.UserInfoLoginPath = strings.TrimSpace(r.FormValue(prefix + "userinfo_login_path"))
		provider.ExtraEmailURL = strings.TrimSpace(r.FormValue(prefix + "extra_email_url"))
		provider.AuthParams = parseKeyValues(r.FormValue(prefix + "auth_params"))
		provider.TokenParams = parseKeyValues(r.FormValue(prefix + "token_params"))
		provider.UserInfoParams = parseKeyValues(r.FormValue(prefix + "userinfo_params"))
		settings.Providers = append(settings.Providers, provider)
	}
	if !settings.LocalPasswordEnabled && readyProviderCount(settings) == 0 {
		handler.renderSettingsResponse(w, r, "", handler.requestText(r, "至少保留一种可用登录方式：本地密码登录，或一个已经填好 client id / secret 的第三方登录。", "Keep at least one working sign-in method: local password or a configured third-party provider."))
		return
	}
	if len(settings.AllowedEmails) == 0 && len(settings.AllowedDomains) == 0 {
		handler.renderSettingsResponse(w, r, "", handler.requestText(r, "请至少配置一个允许登录邮箱或允许登录域名，避免第三方账号无约束进入后台。", "Configure at least one allowed email or domain before enabling console access."))
		return
	}
	if err := handler.store.SaveConsoleAuthSettings(r.Context(), settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.renderSettingsResponse(w, r, handler.requestText(r, "认证设置已保存", "Authentication settings saved"), "")
}

func (handler *Handler) renderSettingsResponse(w http.ResponseWriter, r *http.Request, notice, errorMessage string) {
	data, err := handler.settingsTemplateData(r.Context(), r, notice, errorMessage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "settings-content", data)
		return
	}
	handler.render(w, r, "settings", data)
}

func (handler *Handler) oauthLogin(w http.ResponseWriter, r *http.Request) {
	settings, err := handler.consoleAuthSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	provider, ok := findProvider(settings, chi.URLParam(r, "provider"))
	if !ok || !providerReady(provider) {
		handler.renderLoginPage(w, r, handler.requestText(r, "当前登录方式未启用或配置不完整", "This sign-in method is disabled or incomplete"))
		return
	}
	state, cookieValue, expires, err := newOAuthState(provider.ID, handler.cfg.SecretKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: consoleOAuthStateCookieName, Value: cookieValue, Path: "/console", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	authURL, err := authorizationURL(provider, consoleExternalURL(r, "/console/oauth/"+provider.ID+"/callback"), state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (handler *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	settings, err := handler.consoleAuthSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	provider, ok := findProvider(settings, chi.URLParam(r, "provider"))
	if !ok || !providerReady(provider) {
		handler.renderLoginPage(w, r, handler.requestText(r, "当前登录方式未启用或配置不完整", "This sign-in method is disabled or incomplete"))
		return
	}
	if errorCode := strings.TrimSpace(r.URL.Query().Get("error")); errorCode != "" {
		clearOAuthStateCookie(w)
		handler.renderLoginPage(w, r, handler.requestText(r, "第三方登录失败: ", "Third-party sign-in failed: ")+errorCode)
		return
	}
	stateCookie, err := r.Cookie(consoleOAuthStateCookieName)
	if err != nil || !verifyOAuthStateCookie(stateCookie.Value, provider.ID, r.URL.Query().Get("state"), handler.cfg.SecretKey) {
		clearOAuthStateCookie(w)
		handler.renderLoginPage(w, r, handler.requestText(r, "第三方登录状态校验失败，请重试", "Third-party sign-in state check failed, please try again"))
		return
	}
	clearOAuthStateCookie(w)
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		handler.renderLoginPage(w, r, handler.requestText(r, "第三方登录未返回授权码", "The provider did not return an authorization code"))
		return
	}
	accessToken, err := exchangeAuthorizationCode(r.Context(), provider, code, consoleExternalURL(r, "/console/oauth/"+provider.ID+"/callback"))
	if err != nil {
		handler.renderLoginPage(w, r, handler.requestText(r, "换取 access token 失败: ", "Failed to exchange access token: ")+err.Error())
		return
	}
	identity, err := fetchOAuthIdentity(r.Context(), provider, accessToken)
	if err != nil {
		handler.renderLoginPage(w, r, handler.requestText(r, "读取第三方用户信息失败: ", "Failed to read third-party user info: ")+err.Error())
		return
	}
	if !oauthIdentityAllowed(settings, handler.cfg.AdminEmail, identity) {
		handler.renderLoginPage(w, r, handler.requestText(r, "当前第三方账号未被允许登录后台", "This third-party account is not allowed to access the console"))
		return
	}
	sessionSubject := strings.TrimSpace(identity.Email)
	if sessionSubject == "" {
		sessionSubject = provider.ID + ":" + firstNonEmpty(identity.Login, identity.Subject, identity.Name)
	}
	setSessionCookie(w, sessionSubject, handler.cfg.SecretKey)
	http.Redirect(w, r, "/console/", http.StatusSeeOther)
}

func (handler *Handler) consoleAuthSettings(ctx context.Context) (domain.ConsoleAuthSettings, error) {
	saved, err := handler.store.GetConsoleAuthSettings(ctx)
	if err != nil {
		if err == storage.ErrNotFound {
			return handler.defaultConsoleAuthSettings(), nil
		}
		return domain.ConsoleAuthSettings{}, err
	}
	return mergeConsoleAuthSettings(handler.defaultConsoleAuthSettings(), saved), nil
}

func (handler *Handler) defaultConsoleAuthSettings() domain.ConsoleAuthSettings {
	settings := domain.ConsoleAuthSettings{LocalPasswordEnabled: true, Providers: builtInOAuthProviders()}
	if email := strings.TrimSpace(handler.cfg.AdminEmail); email != "" {
		settings.AllowedEmails = []string{strings.ToLower(email)}
	}
	return settings
}

func builtInOAuthProviders() []domain.OAuthProviderSettings {
	return []domain.OAuthProviderSettings{
		{ID: "google", Name: "Google", Kind: oauthKindOIDC, Scopes: []string{"openid", "email", "profile"}, AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "sub", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "email"},
		{ID: "github", Name: "GitHub", Kind: oauthKindOAuth2, Scopes: []string{"read:user", "user:email"}, AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoURL: "https://api.github.com/user", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "id", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "login", ExtraEmailURL: "https://api.github.com/user/emails"},
		{ID: "gitee", Name: "Gitee", Kind: oauthKindOAuth2, Scopes: []string{"user_info", "emails"}, AuthURL: "https://gitee.com/oauth/authorize", TokenURL: "https://gitee.com/oauth/token", TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoURL: "https://gitee.com/api/v5/user", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "id", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "login"},
		{ID: "feishu", Name: "Feishu", Kind: oauthKindOAuth2, Scopes: []string{"contact:user.base:readonly", "contact:user.email:readonly"}, AuthURL: "https://accounts.feishu.cn/open-apis/authen/v1/authorize", TokenURL: "https://open.feishu.cn/open-apis/authen/v2/oauth/token", TokenStyle: tokenStyleJSON, TokenResponsePath: "data.access_token", AuthStyle: clientAuthParams, UserInfoURL: "https://open.feishu.cn/open-apis/authen/v1/user_info", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "data.open_id", UserInfoEmailPath: "data.email", UserInfoNamePath: "data.name", UserInfoLoginPath: "data.en_name"},
		{ID: "dingtalk", Name: "DingTalk", Kind: oauthKindOAuth2, Scopes: []string{"openid"}, AuthURL: "https://login.dingtalk.com/oauth2/auth", TokenURL: "https://api.dingtalk.com/v1.0/oauth2/userAccessToken", TokenStyle: tokenStyleJSON, TokenResponsePath: "accessToken", AuthStyle: clientAuthParams, UserInfoURL: "https://api.dingtalk.com/v1.0/contact/users/me", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: "header:x-acs-dingtalk-access-token", UserInfoSubjectPath: "unionId", UserInfoEmailPath: "email", UserInfoNamePath: "nick", UserInfoLoginPath: "loginId", AuthParams: []domain.KeyValue{{Key: "prompt", Value: "consent"}}},
		{ID: "gitlab", Name: "GitLab", Kind: oauthKindOIDC, Scopes: []string{"openid", "profile", "email"}, AuthURL: "https://gitlab.com/oauth/authorize", TokenURL: "https://gitlab.com/oauth/token", TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoURL: "https://gitlab.com/oauth/userinfo", UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "sub", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "nickname"},
		{ID: "custom-oidc", Name: "通用 OIDC", Kind: oauthKindOIDC, Scopes: []string{"openid", "email", "profile"}, TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "sub", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "preferred_username"},
		{ID: "custom-oauth", Name: "通用 OAuth2", Kind: oauthKindOAuth2, TokenStyle: tokenStyleForm, TokenResponsePath: defaultTokenResponsePath, AuthStyle: clientAuthParams, UserInfoMethod: userInfoMethodGet, UserInfoTokenStyle: userInfoTokenBearer, UserInfoSubjectPath: "id", UserInfoEmailPath: "email", UserInfoNamePath: "name", UserInfoLoginPath: "login"},
	}
}

func mergeConsoleAuthSettings(defaults, saved domain.ConsoleAuthSettings) domain.ConsoleAuthSettings {
	merged := defaults
	merged.LocalPasswordEnabled = saved.LocalPasswordEnabled
	if len(saved.AllowedEmails) > 0 {
		merged.AllowedEmails = append([]string(nil), saved.AllowedEmails...)
	}
	if len(saved.AllowedDomains) > 0 {
		merged.AllowedDomains = append([]string(nil), saved.AllowedDomains...)
	}
	if !saved.UpdatedAt.IsZero() {
		merged.UpdatedAt = saved.UpdatedAt
	}
	providers := make([]domain.OAuthProviderSettings, 0, len(defaults.Providers)+len(saved.Providers))
	seen := map[string]bool{}
	defaultsByID := map[string]domain.OAuthProviderSettings{}
	for _, provider := range defaults.Providers {
		defaultsByID[provider.ID] = provider
	}
	for _, provider := range defaults.Providers {
		if savedProvider, ok := findProvider(saved, provider.ID); ok {
			providers = append(providers, mergeProviderSettings(provider, savedProvider))
			seen[provider.ID] = true
			continue
		}
		providers = append(providers, provider)
		seen[provider.ID] = true
	}
	for _, provider := range saved.Providers {
		if seen[provider.ID] {
			continue
		}
		if fallback, ok := defaultsByID[provider.ID]; ok {
			providers = append(providers, mergeProviderSettings(fallback, provider))
			continue
		}
		providers = append(providers, provider)
	}
	merged.Providers = providers
	return merged
}

func mergeProviderSettings(defaults, saved domain.OAuthProviderSettings) domain.OAuthProviderSettings {
	merged := defaults
	merged.Enabled = saved.Enabled
	merged.Name = firstNonEmpty(saved.Name, defaults.Name)
	merged.Kind = firstNonEmpty(saved.Kind, defaults.Kind)
	merged.ClientID = firstNonEmpty(saved.ClientID, defaults.ClientID)
	merged.ClientSecret = firstNonEmpty(saved.ClientSecret, defaults.ClientSecret)
	if len(saved.Scopes) > 0 {
		merged.Scopes = append([]string(nil), saved.Scopes...)
	}
	merged.AuthURL = firstNonEmpty(saved.AuthURL, defaults.AuthURL)
	merged.TokenURL = firstNonEmpty(saved.TokenURL, defaults.TokenURL)
	merged.TokenStyle = firstNonEmpty(saved.TokenStyle, defaults.TokenStyle)
	merged.TokenResponsePath = firstNonEmpty(saved.TokenResponsePath, defaults.TokenResponsePath)
	merged.AuthStyle = firstNonEmpty(saved.AuthStyle, defaults.AuthStyle)
	merged.UserInfoURL = firstNonEmpty(saved.UserInfoURL, defaults.UserInfoURL)
	merged.UserInfoMethod = firstNonEmpty(saved.UserInfoMethod, defaults.UserInfoMethod)
	merged.UserInfoTokenStyle = firstNonEmpty(saved.UserInfoTokenStyle, defaults.UserInfoTokenStyle)
	merged.UserInfoSubjectPath = firstNonEmpty(saved.UserInfoSubjectPath, defaults.UserInfoSubjectPath)
	merged.UserInfoEmailPath = firstNonEmpty(saved.UserInfoEmailPath, defaults.UserInfoEmailPath)
	merged.UserInfoNamePath = firstNonEmpty(saved.UserInfoNamePath, defaults.UserInfoNamePath)
	merged.UserInfoLoginPath = firstNonEmpty(saved.UserInfoLoginPath, defaults.UserInfoLoginPath)
	merged.ExtraEmailURL = firstNonEmpty(saved.ExtraEmailURL, defaults.ExtraEmailURL)
	merged.IssuerURL = firstNonEmpty(saved.IssuerURL, defaults.IssuerURL)
	if len(saved.AuthParams) > 0 {
		merged.AuthParams = append([]domain.KeyValue(nil), saved.AuthParams...)
	}
	if len(saved.TokenParams) > 0 {
		merged.TokenParams = append([]domain.KeyValue(nil), saved.TokenParams...)
	}
	if len(saved.UserInfoParams) > 0 {
		merged.UserInfoParams = append([]domain.KeyValue(nil), saved.UserInfoParams...)
	}
	return merged
}

func findProvider(settings domain.ConsoleAuthSettings, providerID string) (domain.OAuthProviderSettings, bool) {
	for _, provider := range settings.Providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return domain.OAuthProviderSettings{}, false
}

func providerReady(provider domain.OAuthProviderSettings) bool {
	return provider.Enabled && strings.TrimSpace(provider.ClientID) != "" && strings.TrimSpace(provider.ClientSecret) != "" && strings.TrimSpace(provider.AuthURL) != "" && strings.TrimSpace(provider.TokenURL) != "" && strings.TrimSpace(provider.UserInfoURL) != ""
}

func readyProviderCount(settings domain.ConsoleAuthSettings) int {
	count := 0
	for _, provider := range settings.Providers {
		if providerReady(provider) {
			count++
		}
	}
	return count
}

func providerKind(provider domain.OAuthProviderSettings) string {
	return firstNonEmpty(provider.Kind, oauthKindOAuth2)
}

func providerAuthStyle(provider domain.OAuthProviderSettings) string {
	return firstNonEmpty(provider.AuthStyle, clientAuthParams)
}

func tokenStyle(provider domain.OAuthProviderSettings) string {
	return firstNonEmpty(provider.TokenStyle, tokenStyleForm)
}

func userInfoMethod(provider domain.OAuthProviderSettings) string {
	return firstNonEmpty(strings.ToUpper(provider.UserInfoMethod), userInfoMethodGet)
}

func splitLinesCSV(value string) []string {
	parts := strings.FieldsFunc(value, func(char rune) bool {
		return char == ',' || char == '\n' || char == '\r' || char == '\t'
	})
	result := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result
}

func parseKeyValues(value string) []domain.KeyValue {
	lines := strings.FieldsFunc(value, func(char rune) bool { return char == '\n' || char == '\r' })
	result := make([]domain.KeyValue, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		result = append(result, domain.KeyValue{Key: key, Value: strings.TrimSpace(parts[1])})
	}
	return result
}

func keyValuesText(values []domain.KeyValue) string {
	if len(values) == 0 {
		return ""
	}
	lines := make([]string, 0, len(values))
	for _, item := range values {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		lines = append(lines, item.Key+"="+item.Value)
	}
	return strings.Join(lines, "\n")
}

func authorizationURL(provider domain.OAuthProviderSettings, redirectURI, state string) (string, error) {
	parsed, err := url.Parse(provider.AuthURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", provider.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	if len(provider.Scopes) > 0 {
		query.Set("scope", strings.Join(provider.Scopes, " "))
	}
	for _, item := range provider.AuthParams {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		query.Set(item.Key, item.Value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeAuthorizationCode(ctx context.Context, provider domain.OAuthProviderSettings, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if providerAuthStyle(provider) != clientAuthBasic {
		form.Set("client_id", provider.ClientID)
		form.Set("client_secret", provider.ClientSecret)
	}
	for _, item := range provider.TokenParams {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		form.Set(item.Key, item.Value)
	}
	var body io.Reader
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.TokenURL, nil)
	if err != nil {
		return "", err
	}
	if tokenStyle(provider) == tokenStyleJSON {
		payload := map[string]string{}
		for key, values := range form {
			if len(values) == 0 {
				continue
			}
			payload[key] = values[0]
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		body = strings.NewReader(string(encoded))
		req.Header.Set("Content-Type", "application/json")
	} else {
		body = strings.NewReader(form.Encode())
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Body = io.NopCloser(body)
	req.ContentLength = -1
	req.Header.Set("Accept", "application/json")
	if providerAuthStyle(provider) == clientAuthBasic {
		req.SetBasicAuth(provider.ClientID, provider.ClientSecret)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, err := decodeJSONResponse(response)
	if err != nil {
		return "", err
	}
	accessToken := jsonPathString(payload, firstNonEmpty(provider.TokenResponsePath, defaultTokenResponsePath))
	if accessToken == "" {
		return "", fmt.Errorf("missing access token")
	}
	return accessToken, nil
}

func fetchOAuthIdentity(ctx context.Context, provider domain.OAuthProviderSettings, accessToken string) (oauthIdentity, error) {
	payload, err := fetchJSONDocument(ctx, userInfoMethod(provider), provider.UserInfoURL, provider.UserInfoParams, firstNonEmpty(provider.UserInfoTokenStyle, userInfoTokenBearer), accessToken)
	if err != nil {
		return oauthIdentity{}, err
	}
	identity := oauthIdentity{
		Subject: jsonPathString(payload, firstNonEmpty(provider.UserInfoSubjectPath, "sub")),
		Email:   strings.ToLower(jsonPathString(payload, firstNonEmpty(provider.UserInfoEmailPath, "email"))),
		Name:    jsonPathString(payload, firstNonEmpty(provider.UserInfoNamePath, "name")),
		Login:   jsonPathString(payload, firstNonEmpty(provider.UserInfoLoginPath, "login")),
	}
	if identity.Email == "" && strings.TrimSpace(provider.ExtraEmailURL) != "" {
		identity.Email = strings.ToLower(fetchGitHubEmail(ctx, provider.ExtraEmailURL, accessToken))
	}
	identity.Subject = firstNonEmpty(identity.Subject, identity.Email, identity.Login, identity.Name)
	if identity.Subject == "" {
		return oauthIdentity{}, fmt.Errorf("missing user identity")
	}
	return identity, nil
}

func fetchGitHubEmail(ctx context.Context, extraEmailURL, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, extraEmailURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ""
	}
	var emails []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&emails); err != nil {
		return ""
	}
	fallback := ""
	for _, email := range emails {
		value := strings.TrimSpace(fmt.Sprint(email["email"]))
		if value == "" {
			continue
		}
		if fallback == "" {
			fallback = value
		}
		primary, _ := email["primary"].(bool)
		verified, _ := email["verified"].(bool)
		if primary && verified {
			return value
		}
	}
	return fallback
}

func fetchJSONDocument(ctx context.Context, method, targetURL string, params []domain.KeyValue, tokenStyle, accessToken string) (any, error) {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	for _, item := range params {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		query.Set(item.Key, item.Value)
	}
	if strings.EqualFold(tokenStyle, userInfoTokenQuery) {
		query.Set("access_token", accessToken)
	}
	parsed.RawQuery = query.Encode()
	var body io.Reader
	if strings.ToUpper(method) == userInfoMethodPost {
		body = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), parsed.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	applyTokenStyle(req, tokenStyle, accessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return decodeJSONResponse(response)
}

func applyTokenStyle(req *http.Request, tokenStyle, accessToken string) {
	trimmed := strings.TrimSpace(tokenStyle)
	if trimmed == "" || strings.EqualFold(trimmed, userInfoTokenBearer) {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		return
	}
	if strings.EqualFold(trimmed, userInfoTokenQuery) {
		return
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "header:") {
		headerName := strings.TrimSpace(trimmed[len("header:"):])
		if headerName != "" {
			req.Header.Set(headerName, accessToken)
			return
		}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
}

func decodeJSONResponse(response *http.Response) (any, error) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return payload, nil
}

func jsonPathString(payload any, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	current := payload
	for _, segment := range strings.Split(path, ".") {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			return ""
		}
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[trimmed]
		if !ok {
			return ""
		}
	}
	switch value := current.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		if current == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(current))
	}
}

func oauthIdentityAllowed(settings domain.ConsoleAuthSettings, adminEmail string, identity oauthIdentity) bool {
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	login := strings.ToLower(strings.TrimSpace(identity.Login))
	if email != "" && email == strings.ToLower(strings.TrimSpace(adminEmail)) {
		return true
	}
	for _, allowed := range settings.AllowedEmails {
		normalized := strings.ToLower(strings.TrimSpace(allowed))
		if normalized == "" {
			continue
		}
		if normalized == email || normalized == login || normalized == strings.ToLower(strings.TrimSpace(identity.Subject)) {
			return true
		}
	}
	if email == "" {
		return false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	domainName := parts[1]
	for _, allowed := range settings.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(allowed), domainName) {
			return true
		}
	}
	return false
}

func newOAuthState(providerID, secret string) (string, string, time.Time, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", time.Time{}, err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(10 * time.Minute).UTC()
	payload := providerID + ":" + state + ":" + strconv.FormatInt(expires.Unix(), 10)
	signature := auth.Sign(payload, secret)
	return state, payload + ":" + signature, expires, nil
}

func verifyOAuthStateCookie(value, providerID, state, secret string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 4 {
		return false
	}
	payload := strings.Join(parts[:3], ":")
	if !auth.Verify(payload, parts[3], secret) {
		return false
	}
	if parts[0] != providerID || parts[1] != strings.TrimSpace(state) {
		return false
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil && time.Now().Unix() < expires
}

func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: consoleOAuthStateCookieName, Value: "", Path: "/console", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func consoleExternalURL(r *http.Request, path string) string {
	scheme := forwardedHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := forwardedHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + path
}

func forwardedHeaderValue(value string) string {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
