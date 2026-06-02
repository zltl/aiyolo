package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const launcherConfigEnv = "AIYOLO_CONFIG_PATH"

type launcherConfig struct {
	APIBaseURL    string   `json:"api_base_url"`
	APIKey        string   `json:"api_key"`
	DefaultModel  string   `json:"default_model"`
	AllowedModels []string `json:"allowed_models"`
	CodexCommand  string   `json:"codex_command"`
}

type doctorResult struct {
	ModelCount int
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:          "aiyolo",
		Short:        "AIYolo Codex wrapper",
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "Path to the local AIYolo wrapper config file")
	root.AddCommand(newDoctorCommand(&configPath))
	root.AddCommand(newConfigCommand(&configPath))
	root.AddCommand(newCodexCommand(&configPath))
	return root
}

func newDoctorCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify local AIYolo wrapper configuration and gateway access",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, resolvedPath, err := loadLauncherConfig(*configPath)
			if err != nil {
				return err
			}
			result, err := runDoctor(cmd.Context(), cfg, http.DefaultClient)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "config=%s\nmodels=%d\nstatus=ok\n", resolvedPath, result.ModelCount)
			return nil
		},
	}
}

func newConfigCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the current local AIYolo wrapper configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, resolvedPath, err := loadLauncherConfig(*configPath)
			if err != nil {
				return err
			}
			payload, err := json.MarshalIndent(cfg.redacted(), "", "  ")
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "config=%s\n%s\n", resolvedPath, payload)
			return nil
		},
	}
}

func newCodexCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:                "codex [args...]",
		Short:              "Launch Codex with AIYolo credentials injected",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := loadLauncherConfig(*configPath)
			if err != nil {
				return err
			}
			if err := validateLauncherConfig(cfg); err != nil {
				return err
			}
			codexCommand := firstNonEmpty(os.Getenv("AIYOLO_CODEX_COMMAND"), cfg.CodexCommand, "codex")
			execCmd := exec.CommandContext(cmd.Context(), codexCommand, args...)
			execCmd.Stdin = os.Stdin
			execCmd.Stdout = os.Stdout
			execCmd.Stderr = os.Stderr
			execCmd.Env = codexCommandEnv(os.Environ(), cfg)
			return execCmd.Run()
		},
	}
}

func loadLauncherConfig(path string) (launcherConfig, string, error) {
	resolvedPath, err := resolveLauncherConfigPath(path)
	if err != nil {
		return launcherConfig{}, "", err
	}
	payload, err := os.ReadFile(resolvedPath)
	if err != nil {
		return launcherConfig{}, resolvedPath, err
	}
	var cfg launcherConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return launcherConfig{}, resolvedPath, err
	}
	return cfg, resolvedPath, validateLauncherConfig(cfg)
}

func resolveLauncherConfigPath(path string) (string, error) {
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		return trimmed, nil
	}
	if trimmed := strings.TrimSpace(os.Getenv(launcherConfigEnv)); trimmed != "" {
		return trimmed, nil
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		return filepath.Join(localAppData, "AIYolo", "config.json"), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "aiyolo", "config.json"), nil
}

func runDoctor(ctx context.Context, cfg launcherConfig, client *http.Client) (doctorResult, error) {
	if err := validateLauncherConfig(cfg); err != nil {
		return doctorResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.APIBaseURL, "/")+"/models", nil)
	if err != nil {
		return doctorResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	response, err := client.Do(request)
	if err != nil {
		return doctorResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return doctorResult{}, fmt.Errorf("doctor request failed status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return doctorResult{}, err
	}
	return doctorResult{ModelCount: len(payload.Data)}, nil
}

func validateLauncherConfig(cfg launcherConfig) error {
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		return errors.New("api_base_url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("api_key is required")
	}
	return nil
}

func codexCommandEnv(baseEnv []string, cfg launcherConfig) []string {
	env := append([]string{}, baseEnv...)
	env = append(env,
		"OPENAI_API_KEY="+cfg.APIKey,
		"CODEX_API_KEY="+cfg.APIKey,
		"OPENAI_BASE_URL="+cfg.APIBaseURL,
	)
	if strings.TrimSpace(cfg.DefaultModel) != "" {
		env = append(env, "AIYOLO_DEFAULT_MODEL="+strings.TrimSpace(cfg.DefaultModel))
	}
	return env
}

func (cfg launcherConfig) redacted() launcherConfig {
	copy := cfg
	copy.APIKey = redactAPIKey(copy.APIKey)
	return copy
}

func redactAPIKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:8] + "..." + value[len(value)-4:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
