package workers

import (
	"fmt"
	"strings"
)

const cloudAgentBrowserMCPConfigPath = "/workspace/.mcp.json"

// CloudAgentBrowserMCPConfig holds Claude Code MCP settings for the container browser.
type CloudAgentBrowserMCPConfig struct {
	URL   string
	Token string
	Path  string
}

func normalizeCloudAgentBrowserMCPConfig(config CloudAgentBrowserMCPConfig) CloudAgentBrowserMCPConfig {
	config.URL = strings.TrimSpace(config.URL)
	config.Token = strings.TrimSpace(config.Token)
	config.Path = strings.TrimSpace(config.Path)
	if config.Path == "" {
		config.Path = cloudAgentBrowserMCPConfigPath
	}
	return config
}

func jsonStringLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

// BuildCloudAgentBrowserMCPClearShell returns a shell snippet that removes Claude Code MCP config.
func BuildCloudAgentBrowserMCPClearShell() string {
	return fmt.Sprintf("rm -f %s\n", shellQuote(cloudAgentBrowserMCPConfigPath))
}

// BuildCloudAgentBrowserMCPConfiguredShell returns a shell snippet that prints yes/no for MCP config presence.
func BuildCloudAgentBrowserMCPConfiguredShell() string {
	return fmt.Sprintf("if [[ -f %s ]]; then echo yes; else echo no; fi\n", shellQuote(cloudAgentBrowserMCPConfigPath))
}

func buildCloudAgentBrowserMCPConfigShell(config CloudAgentBrowserMCPConfig) string {
	return BuildCloudAgentBrowserMCPConfigShell(config)
}

// BuildCloudAgentBrowserMCPConfigShell returns a shell snippet that writes Claude Code MCP config.
func BuildCloudAgentBrowserMCPConfigShell(config CloudAgentBrowserMCPConfig) string {
	config = normalizeCloudAgentBrowserMCPConfig(config)
	if config.URL == "" || config.Token == "" {
		return ""
	}
	return fmt.Sprintf(`if [[ -n %s && -n %s ]]; then
  cat > %s <<'AIYOLO_MCP_JSON'
{
  "mcpServers": {
    "aiyolo-browser": {
      "type": "http",
      "url": %s,
      "headers": {
        "Authorization": %s
      }
    }
  }
}
AIYOLO_MCP_JSON
fi
`, shellQuote(config.URL), shellQuote(config.Token), shellQuote(config.Path), jsonStringLiteral(config.URL), jsonStringLiteral("Bearer "+config.Token))
}
