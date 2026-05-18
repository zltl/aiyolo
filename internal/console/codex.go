package console

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

var codexDefaultAllowedModels = []string{"gpt-5.4", "gpt-5.5", "gpt-5.5-pro"}

func (handler *Handler) codex(w http.ResponseWriter, r *http.Request) {
	handler.renderCodexPage(w, r, normalizeCodexDefaultModel(""), "", "")
}

func (handler *Handler) createCodexInstallToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	baseURL := handler.codexPublicBaseURL(r)
	if baseURL == "" {
		http.Error(w, handler.requestText(r, "无法解析当前 AIYolo 访问地址", "Unable to resolve the current AIYolo public URL"), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	clearToken, err := generateCodexOpaqueToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defaultModel := normalizeCodexDefaultModel(r.FormValue("default_model"))
	token := domain.CodexInstallToken{
		ID:            newConsoleID("codex_install"),
		TokenHash:     auth.HashAPIKey(clearToken),
		CreatedBy:     "local",
		Platform:      "windows",
		DefaultModel:  defaultModel,
		AllowedModels: cloneStrings(codexDefaultAllowedModels),
		ExpiresAt:     now.Add(handler.cfg.CodexInstallTokenTTL),
		CreatedAt:     now,
	}
	if err := handler.store.CreateCodexInstallToken(r.Context(), token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	command := "irm " + psQuote(baseURL+"/console/codex/install.ps1?token="+clearToken) + " | iex"
	handler.renderCodexPage(w, r, defaultModel, command, handler.requestText(r, "Windows 安装命令已生成，链接只可使用一次。", "Windows install command generated. The link can be used only once."))
}

func (handler *Handler) codexInstallScript(w http.ResponseWriter, r *http.Request) {
	clearToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if clearToken == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	baseURL := handler.codexPublicBaseURL(r)
	if baseURL == "" {
		http.Error(w, "missing public base URL", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	clearKey, key, err := newConsoleAPIKey(apiKeySpec{
		Name:             "Codex Windows " + now.Format("2006-01-02 15:04:05"),
		Kind:             "live",
		AllowedProtocols: []string{domain.ProtocolOpenAI},
		AllowedModels:    cloneStrings(codexDefaultAllowedModels),
		CreatedAt:        now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redeemed, err := handler.store.RedeemCodexInstallToken(r.Context(), auth.HashAPIKey(clearToken), key, now)
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "install token is invalid, expired, or already used", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	script := renderCodexWindowsInstallScript(codexInstallScriptData{
		WrapperURL:    handler.resolveCodexWrapperURL(baseURL),
		WrapperSHA256: strings.TrimSpace(handler.cfg.CodexWindowsWrapperSHA256),
		APIBaseURL:    baseURL + "/v1",
		APIKey:        clearKey,
		DefaultModel:  normalizeCodexDefaultModel(redeemed.DefaultModel),
		AllowedModels: redeemed.AllowedModels,
	})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}

func (handler *Handler) codexWindowsArtifact(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(handler.cfg.CodexWindowsWrapperURL)
	if target == "" {
		target = "/console/codex/artifacts/aiyolo.exe"
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		copyHeaderIfPresent(w.Header(), response.Header, "Content-Type")
		copyHeaderIfPresent(w.Header(), response.Header, "Content-Length")
		copyHeaderIfPresent(w.Header(), response.Header, "ETag")
		copyHeaderIfPresent(w.Header(), response.Header, "Last-Modified")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	if strings.HasPrefix(target, "/") && target != "/console/codex/artifacts/aiyolo.exe" {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	http.Error(w, "Windows wrapper artifact is not configured yet", http.StatusNotFound)
}

func (handler *Handler) renderCodexPage(w http.ResponseWriter, r *http.Request, defaultModel string, command string, notice string) {
	releases, catalogIndexPath, catalogError := handler.codexArtifactReleases(r)
	data := map[string]any{
		"Title":                "Codex",
		"AllowedModels":        cloneStrings(codexDefaultAllowedModels),
		"DefaultModel":         normalizeCodexDefaultModel(defaultModel),
		"InstallCommand":       command,
		"Notice":               notice,
		"TTLMinutes":           int(handler.cfg.CodexInstallTokenTTL.Minutes()),
		"WrapperURL":           handler.cfg.CodexWindowsWrapperURL,
		"WrapperSHA256":        handler.cfg.CodexWindowsWrapperSHA256,
		"ArtifactIndexPath":    catalogIndexPath,
		"ArtifactCatalogError": catalogError,
		"ArtifactReleases":     releases,
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "codex-content", data)
		return
	}
	handler.render(w, r, "codex", data)
}

func (handler *Handler) codexArtifactReleases(r *http.Request) ([]artifacts.ReleaseView, string, string) {
	indexPath := ""
	if handler.cfg.Artifacts.CanList() {
		indexPath = handler.cfg.Artifacts.NormalizedProxyBasePath() + "/index.json"
	}
	if !handler.cfg.Artifacts.CanList() || r == nil {
		return nil, indexPath, ""
	}
	reader, err := artifacts.NewCatalogReader(handler.cfg.Artifacts)
	if err != nil {
		return nil, indexPath, err.Error()
	}
	catalog, err := reader.Catalog(r.Context(), "")
	if err != nil {
		return nil, indexPath, err.Error()
	}
	return artifacts.BuildReleaseViews(handler.cfg.Artifacts.NormalizedProxyBasePath(), catalog.Entries, "windows", "aiyolo.exe"), indexPath, ""
}

func (handler *Handler) codexPublicBaseURL(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(handler.cfg.CodexPublicBaseURL), "/"); configured != "" {
		return configured
	}
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := strings.ToLower(forwardedHeaderValue(r.Header.Get("x-forwarded-proto"))); forwardedProto == "http" || forwardedProto == "https" {
		scheme = forwardedProto
	}
	host := forwardedHeaderValue(r.Header.Get("x-forwarded-host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func (handler *Handler) resolveCodexWrapperURL(baseURL string) string {
	target := strings.TrimSpace(handler.cfg.CodexWindowsWrapperURL)
	if target == "" {
		target = "/console/codex/artifacts/aiyolo.exe"
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if strings.HasPrefix(target, "/") {
		return strings.TrimRight(baseURL, "/") + target
	}
	return strings.TrimRight(baseURL, "/") + "/" + target
}

func generateCodexOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "codex_" + hex.EncodeToString(raw), nil
}

func normalizeCodexDefaultModel(value string) string {
	value = strings.TrimSpace(value)
	for _, model := range codexDefaultAllowedModels {
		if value == model {
			return value
		}
	}
	return "gpt-5.5"
}

type codexInstallScriptData struct {
	WrapperURL    string
	WrapperSHA256 string
	APIBaseURL    string
	APIKey        string
	DefaultModel  string
	AllowedModels []string
}

func renderCodexWindowsInstallScript(data codexInstallScriptData) string {
	var builder strings.Builder
	builder.WriteString("# AIYolo Codex Windows installer\n")
	builder.WriteString("$ErrorActionPreference = 'Stop'\n")
	builder.WriteString("$installRoot = Join-Path $env:LOCALAPPDATA 'AIYolo'\n")
	builder.WriteString("$binRoot = Join-Path $installRoot 'bin'\n")
	builder.WriteString("$configPath = Join-Path $installRoot 'config.json'\n")
	builder.WriteString("New-Item -ItemType Directory -Force -Path $binRoot | Out-Null\n")
	builder.WriteString("$wrapperUrl = " + psQuote(data.WrapperURL) + "\n")
	builder.WriteString("$exePath = Join-Path $binRoot 'aiyolo.exe'\n")
	builder.WriteString("Invoke-WebRequest -Uri $wrapperUrl -OutFile $exePath -UseBasicParsing\n")
	if strings.TrimSpace(data.WrapperSHA256) != "" {
		builder.WriteString("$expectedHash = " + psQuote(strings.ToUpper(strings.TrimSpace(data.WrapperSHA256))) + "\n")
		builder.WriteString("$actualHash = (Get-FileHash -Path $exePath -Algorithm SHA256).Hash.ToUpperInvariant()\n")
		builder.WriteString("if ($actualHash -ne $expectedHash) { throw \"aiyolo.exe SHA256 mismatch: $actualHash\" }\n")
	}
	builder.WriteString("$config = [ordered]@{\n")
	builder.WriteString("  api_base_url = " + psQuote(data.APIBaseURL) + "\n")
	builder.WriteString("  api_key = " + psQuote(data.APIKey) + "\n")
	builder.WriteString("  default_model = " + psQuote(data.DefaultModel) + "\n")
	builder.WriteString("  allowed_models = @(")
	for index, model := range data.AllowedModels {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(psQuote(model))
	}
	builder.WriteString(")\n")
	builder.WriteString("  codex_command = 'codex'\n")
	builder.WriteString("}\n")
	builder.WriteString("$config | ConvertTo-Json -Depth 5 | Set-Content -Path $configPath -Encoding UTF8\n")
	builder.WriteString("$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')\n")
	builder.WriteString("if (($userPath -split ';') -notcontains $binRoot) { [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $binRoot).Trim(';')), 'User') }\n")
	builder.WriteString("Write-Host 'AIYolo Codex configuration written to' $configPath\n")
	builder.WriteString("& $exePath doctor\n")
	return builder.String()
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func copyHeaderIfPresent(dst http.Header, src http.Header, key string) {
	if value := strings.TrimSpace(src.Get(key)); value != "" {
		dst.Set(key, value)
	}
}
