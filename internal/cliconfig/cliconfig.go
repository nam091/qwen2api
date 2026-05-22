package cliconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ToolStatus represents the configuration and installation status of a CLI tool.
type ToolStatus struct {
	Installed    bool           `json:"installed"`
	HasConfig    bool           `json:"has_config"`
	ConfigPath   string         `json:"config_path"`
	Settings     map[string]any `json:"settings,omitempty"`
}

// CheckClaude checks if Claude CLI is installed or configured.
func CheckClaude() (ToolStatus, error) {
	status := ToolStatus{Installed: false, HasConfig: false}
	home, err := os.UserHomeDir()
	if err != nil {
		return status, fmt.Errorf("user home dir: %w", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	status.ConfigPath = settingsPath

	// Check PATH
	_, lookErr := exec.LookPath("claude")
	if lookErr == nil {
		status.Installed = true
	}

	// Check if settings file exists
	content, err := os.ReadFile(settingsPath)
	if err == nil {
		status.Installed = true // If config file exists, consider it installed
		var settings map[string]any
		if err := json.Unmarshal(content, &settings); err == nil {
			status.Settings = settings
			if env, ok := settings["env"].(map[string]any); ok {
				if _, hasUrl := env["ANTHROPIC_BASE_URL"]; hasUrl {
					status.HasConfig = true
				}
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, fmt.Errorf("read settings: %w", err)
	}

	return status, nil
}

// ApplyClaude configures Claude CLI env values.
func ApplyClaude(baseURL, apiKey, sonnetModel, opusModel, haikuModel string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings := make(map[string]any)

	content, err := os.ReadFile(settingsPath)
	if err == nil {
		_ = json.Unmarshal(content, &settings)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read settings: %w", err)
	}

	// Ensure baseURL ends with /v1
	normalizedBase := baseURL
	if normalizedBase != "" && !strings.HasSuffix(normalizedBase, "/v1") {
		if strings.HasSuffix(normalizedBase, "/") {
			normalizedBase += "v1"
		} else {
			normalizedBase += "/v1"
		}
	}

	env, ok := settings["env"].(map[string]any)
	if !ok {
		env = make(map[string]any)
	}

	env["ANTHROPIC_BASE_URL"] = normalizedBase
	env["ANTHROPIC_AUTH_TOKEN"] = apiKey
	env["API_TIMEOUT_MS"] = "600000"

	if sonnetModel != "" {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = sonnetModel
	}
	if opusModel != "" {
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = opusModel
	}
	if haikuModel != "" {
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = haikuModel
	}

	settings["env"] = env
	settings["hasCompletedOnboarding"] = true

	updatedContent, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	return nil
}

// ResetClaude removes Qwen2API variables from Claude CLI configuration.
func ResetClaude() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		return fmt.Errorf("unmarshal settings: %w", err)
	}

	env, ok := settings["env"].(map[string]any)
	if ok {
		delete(env, "ANTHROPIC_BASE_URL")
		delete(env, "ANTHROPIC_AUTH_TOKEN")
		delete(env, "ANTHROPIC_DEFAULT_SONNET_MODEL")
		delete(env, "ANTHROPIC_DEFAULT_OPUS_MODEL")
		delete(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL")
		delete(env, "API_TIMEOUT_MS")

		if len(env) == 0 {
			delete(settings, "env")
		} else {
			settings["env"] = env
		}
	}

	updatedContent, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	return nil
}

// CheckCline checks if Cline is installed or configured.
func CheckCline() (ToolStatus, error) {
	status := ToolStatus{Installed: false, HasConfig: false}
	home, err := os.UserHomeDir()
	if err != nil {
		return status, fmt.Errorf("user home dir: %w", err)
	}

	dataDir := filepath.Join(home, ".cline", "data")
	globalStatePath := filepath.Join(dataDir, "globalState.json")
	status.ConfigPath = globalStatePath

	// Check PATH
	_, lookErr := exec.LookPath("cline")
	if lookErr == nil {
		status.Installed = true
	}

	// Check if config exists
	content, err := os.ReadFile(globalStatePath)
	if err == nil {
		status.Installed = true
		var globalState map[string]any
		if err := json.Unmarshal(content, &globalState); err == nil {
			status.Settings = make(map[string]any)
			status.Settings["actModeApiProvider"] = globalState["actModeApiProvider"]
			status.Settings["planModeApiProvider"] = globalState["planModeApiProvider"]
			status.Settings["openAiBaseUrl"] = globalState["openAiBaseUrl"]
			status.Settings["openAiModelId"] = globalState["openAiModelId"]

			provider, _ := globalState["actModeApiProvider"].(string)
			baseUrl, _ := globalState["openAiBaseUrl"].(string)
			if provider == "openai" && (strings.Contains(baseUrl, "localhost") || strings.Contains(baseUrl, "127.0.0.1") || strings.Contains(baseUrl, "qwen")) {
				status.HasConfig = true
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, fmt.Errorf("read globalState: %w", err)
	}

	return status, nil
}

// ApplyCline configures Cline CLI to use Qwen2API OpenAI-compatible endpoints.
func ApplyCline(baseURL, apiKey, model string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}

	dataDir := filepath.Join(home, ".cline", "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	// Cline expects baseURL WITHOUT /v1 suffix
	normalizedBase := baseURL
	if strings.HasSuffix(normalizedBase, "/v1") {
		normalizedBase = strings.TrimSuffix(normalizedBase, "/v1")
	} else if strings.HasSuffix(normalizedBase, "/v1/") {
		normalizedBase = strings.TrimSuffix(normalizedBase, "/v1/")
	}

	globalStatePath := filepath.Join(dataDir, "globalState.json")
	globalState := make(map[string]any)

	content, err := os.ReadFile(globalStatePath)
	if err == nil {
		_ = json.Unmarshal(content, &globalState)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read globalState: %w", err)
	}

	globalState["actModeApiProvider"] = "openai"
	globalState["planModeApiProvider"] = "openai"
	globalState["openAiBaseUrl"] = normalizedBase
	globalState["openAiModelId"] = model
	globalState["planModeOpenAiModelId"] = model

	updatedGlobalState, err := json.MarshalIndent(globalState, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal globalState: %w", err)
	}

	if err := os.WriteFile(globalStatePath, updatedGlobalState, 0644); err != nil {
		return fmt.Errorf("write globalState: %w", err)
	}

	// Save secrets.json
	secretsPath := filepath.Join(dataDir, "secrets.json")
	secrets := make(map[string]any)

	secretsContent, err := os.ReadFile(secretsPath)
	if err == nil {
		_ = json.Unmarshal(secretsContent, &secrets)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read secrets: %w", err)
	}

	secrets["openAiApiKey"] = apiKey

	updatedSecrets, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}

	if err := os.WriteFile(secretsPath, updatedSecrets, 0644); err != nil {
		return fmt.Errorf("write secrets: %w", err)
	}

	return nil
}

// ResetCline resets Cline CLI configurations.
func ResetCline() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}

	dataDir := filepath.Join(home, ".cline", "data")
	globalStatePath := filepath.Join(dataDir, "globalState.json")

	content, err := os.ReadFile(globalStatePath)
	if err == nil {
		var globalState map[string]any
		if err := json.Unmarshal(content, &globalState); err == nil {
			provider, _ := globalState["actModeApiProvider"].(string)
			if provider == "openai" {
				delete(globalState, "openAiBaseUrl")
				delete(globalState, "openAiModelId")
				delete(globalState, "planModeOpenAiModelId")
				globalState["actModeApiProvider"] = "cline"
				globalState["planModeApiProvider"] = "cline"

				updated, err := json.MarshalIndent(globalState, "", "  ")
				if err == nil {
					_ = os.WriteFile(globalStatePath, updated, 0644)
				}
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read globalState: %w", err)
	}

	secretsPath := filepath.Join(dataDir, "secrets.json")
	secretsContent, err := os.ReadFile(secretsPath)
	if err == nil {
		var secrets map[string]any
		if err := json.Unmarshal(secretsContent, &secrets); err == nil {
			delete(secrets, "openAiApiKey")
			updated, err := json.MarshalIndent(secrets, "", "  ")
			if err == nil {
				_ = os.WriteFile(secretsPath, updated, 0644)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read secrets: %w", err)
	}

	return nil
}

// CheckDeepSeek checks if DeepSeek TUI is installed or configured.
func CheckDeepSeek() (ToolStatus, error) {
	status := ToolStatus{Installed: false, HasConfig: false}
	home, err := os.UserHomeDir()
	if err != nil {
		return status, fmt.Errorf("user home dir: %w", err)
	}
	configPath := filepath.Join(home, ".deepseek", "config.toml")
	status.ConfigPath = configPath

	_, lookErr := exec.LookPath("deepseek")
	if lookErr == nil {
		status.Installed = true
	}

	content, err := os.ReadFile(configPath)
	if err == nil {
		status.Installed = true
		status.Settings = make(map[string]any)

		// Simple line-by-line parsing
		lines := strings.Split(string(content), "\n")
		var provider string
		var inOpenAiSection bool
		var baseUrl, apiKey, model string

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section := strings.Trim(line, "[]")
				inOpenAiSection = (section == "providers.openai")
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"`)

			if !inOpenAiSection {
				if key == "provider" {
					provider = val
				}
			} else {
				if key == "base_url" {
					baseUrl = val
				} else if key == "api_key" {
					apiKey = val
				} else if key == "model" {
					model = val
				}
			}
		}

		status.Settings["provider"] = provider
		status.Settings["base_url"] = baseUrl
		status.Settings["api_key"] = apiKey
		status.Settings["model"] = model

		if provider == "openai" && (strings.Contains(baseUrl, "localhost") || strings.Contains(baseUrl, "127.0.0.1") || strings.Contains(baseUrl, "qwen")) {
			status.HasConfig = true
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, fmt.Errorf("read settings: %w", err)
	}

	return status, nil
}

// ApplyDeepSeek configures DeepSeek TUI settings.
func ApplyDeepSeek(baseURL, apiKey, model string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	deepseekDir := filepath.Join(home, ".deepseek")
	if err := os.MkdirAll(deepseekDir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	configPath := filepath.Join(deepseekDir, "config.toml")

	normalizedBase := baseURL
	if normalizedBase != "" && !strings.HasSuffix(normalizedBase, "/v1") {
		if strings.HasSuffix(normalizedBase, "/") {
			normalizedBase += "v1"
		} else {
			normalizedBase += "/v1"
		}
	}

	tomlContent := fmt.Sprintf(`provider = "openai"

[providers.openai]
base_url = "%s"
api_key = "%s"
model = "%s"
`, normalizedBase, apiKey, model)

	if err := os.WriteFile(configPath, []byte(tomlContent), 0644); err != nil {
		return fmt.Errorf("write deepseek config: %w", err)
	}
	return nil
}

// ResetDeepSeek resets DeepSeek TUI settings back to defaults.
func ResetDeepSeek() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	configPath := filepath.Join(home, ".deepseek", "config.toml")

	tomlContent := `provider = "deepseek"
`
	if err := os.WriteFile(configPath, []byte(tomlContent), 0644); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reset deepseek config: %w", err)
	}
	return nil
}

// CheckQwen checks if Qwen Code CLI is installed or configured.
func CheckQwen() (ToolStatus, error) {
	status := ToolStatus{Installed: false, HasConfig: false}
	home, err := os.UserHomeDir()
	if err != nil {
		return status, fmt.Errorf("user home dir: %w", err)
	}
	settingsPath := filepath.Join(home, ".qwen", "settings.json")
	status.ConfigPath = settingsPath

	_, lookErr := exec.LookPath("qwen")
	if lookErr == nil {
		status.Installed = true
	}

	content, err := os.ReadFile(settingsPath)
	if err == nil {
		status.Installed = true
		var settings map[string]any
		if err := json.Unmarshal(content, &settings); err == nil {
			status.Settings = settings

			if security, ok := settings["security"].(map[string]any); ok {
				if auth, ok := security["auth"].(map[string]any); ok {
					selectedType, _ := auth["selectedType"].(string)
					baseUrl, _ := auth["baseUrl"].(string)
					if selectedType == "openai" && (strings.Contains(baseUrl, "localhost") || strings.Contains(baseUrl, "127.0.0.1") || strings.Contains(baseUrl, "qwen")) {
						status.HasConfig = true
					}
				}
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, fmt.Errorf("read settings: %w", err)
	}

	return status, nil
}

// ApplyQwen configures Qwen Code CLI settings.
func ApplyQwen(baseURL, apiKey, model string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	qwenDir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(qwenDir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	settingsPath := filepath.Join(qwenDir, "settings.json")

	settings := make(map[string]any)
	content, err := os.ReadFile(settingsPath)
	if err == nil {
		_ = json.Unmarshal(content, &settings)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read settings: %w", err)
	}

	normalizedBase := baseURL
	if normalizedBase != "" && !strings.HasSuffix(normalizedBase, "/v1") {
		if strings.HasSuffix(normalizedBase, "/") {
			normalizedBase += "v1"
		} else {
			normalizedBase += "/v1"
		}
	}

	security, ok := settings["security"].(map[string]any)
	if !ok {
		security = make(map[string]any)
	}
	auth, ok := security["auth"].(map[string]any)
	if !ok {
		auth = make(map[string]any)
	}

	auth["selectedType"] = "openai"
	auth["apiKey"] = apiKey
	auth["baseUrl"] = normalizedBase

	security["auth"] = auth
	settings["security"] = security

	modelSec, ok := settings["model"].(map[string]any)
	if !ok {
		modelSec = make(map[string]any)
	}
	modelSec["name"] = model
	settings["model"] = modelSec

	updatedContent, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// ResetQwen resets Qwen Code CLI settings.
func ResetQwen() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	settingsPath := filepath.Join(home, ".qwen", "settings.json")

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		return fmt.Errorf("unmarshal settings: %w", err)
	}

	if security, ok := settings["security"].(map[string]any); ok {
		if auth, ok := security["auth"].(map[string]any); ok {
			auth["selectedType"] = "qwen"
			delete(auth, "apiKey")
			delete(auth, "baseUrl")
		}
	}

	updatedContent, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

