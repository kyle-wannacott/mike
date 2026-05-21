package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.design/x/hotkey"
)

// Config holds all application configuration.
type Config struct {
	// Path to the whisper model file (ggml format, e.g. ggml-tiny.bin)
	ModelPath string `json:"model_path"`
	// Path to Silero VAD model (optional, enables voice activity detection)
	VADModelPath string `json:"vad_model_path"`
	// Language for transcription (e.g. "en", "auto")
	Language string `json:"language"`
	// Number of CPU threads to use for inference
	Threads int `json:"threads"`
	// Maximum recording duration in seconds
	MaxDuration int `json:"max_duration_seconds"`
	// Hotkey modifier keys (e.g. ["ctrl", "shift"])
	// Available: ctrl, shift, mod1, mod2, mod3, mod4, mod5
	HotkeyModifiers []string `json:"hotkey_modifiers"`
	// Hotkey main key (e.g. "space", "v", "r", "f1"-"f20")
	HotkeyKey string `json:"hotkey_key"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		ModelPath:       filepath.Join(home, ".config", "mike", "models", "ggml-tiny.bin"),
		VADModelPath:    filepath.Join(home, ".config", "mike", "models", "ggml-silero-vad.bin"),
		Language:        "en",
		Threads:         4,
		MaxDuration:     30,
		HotkeyModifiers: []string{"ctrl"},
		HotkeyKey:       "space",
	}
}

// ConfigDir returns the directory where configuration is stored.
func ConfigDir() string {
	if d := os.Getenv("MIKE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mike")
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// ModelsDir returns the path to the models directory.
func ModelsDir() string {
	return filepath.Join(ConfigDir(), "models")
}

// EnsureDirs creates the configuration and models directories.
func EnsureDirs() error {
	for _, d := range []string{ConfigDir(), ModelsDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}
	return nil
}

// Save writes the configuration to disk.
func (c *Config) Save() error {
	if err := EnsureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(ConfigPath(), data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// LoadConfig loads configuration from disk, returning defaults if none exists.
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			// Save defaults
			if e := cfg.Save(); e != nil {
				return cfg, fmt.Errorf("saving default config: %w", e)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// modMap maps string modifier names to hotkey.Modifier values.
// Only modifiers available on Linux/X11 are included.
var modMap = map[string]hotkey.Modifier{
	"ctrl":  hotkey.ModCtrl,
	"shift": hotkey.ModShift,
	"mod1":  hotkey.Mod1,
	"mod2":  hotkey.Mod2,
	"mod3":  hotkey.Mod3,
	"mod4":  hotkey.Mod4,
	"mod5":  hotkey.Mod5,
}

// keyMap maps string key names to hotkey.Key values.
// Only keys available on Linux/X11 are included.
var keyMap = map[string]hotkey.Key{
	"a":      hotkey.KeyA,
	"b":      hotkey.KeyB,
	"c":      hotkey.KeyC,
	"d":      hotkey.KeyD,
	"e":      hotkey.KeyE,
	"f":      hotkey.KeyF,
	"g":      hotkey.KeyG,
	"h":      hotkey.KeyH,
	"i":      hotkey.KeyI,
	"j":      hotkey.KeyJ,
	"k":      hotkey.KeyK,
	"l":      hotkey.KeyL,
	"m":      hotkey.KeyM,
	"n":      hotkey.KeyN,
	"o":      hotkey.KeyO,
	"p":      hotkey.KeyP,
	"q":      hotkey.KeyQ,
	"r":      hotkey.KeyR,
	"s":      hotkey.KeyS,
	"t":      hotkey.KeyT,
	"u":      hotkey.KeyU,
	"v":      hotkey.KeyV,
	"w":      hotkey.KeyW,
	"x":      hotkey.KeyX,
	"y":      hotkey.KeyY,
	"z":      hotkey.KeyZ,
	"0":      hotkey.Key0,
	"1":      hotkey.Key1,
	"2":      hotkey.Key2,
	"3":      hotkey.Key3,
	"4":      hotkey.Key4,
	"5":      hotkey.Key5,
	"6":      hotkey.Key6,
	"7":      hotkey.Key7,
	"8":      hotkey.Key8,
	"9":      hotkey.Key9,
	"space":  hotkey.KeySpace,
	"enter":  hotkey.KeyReturn,
	"return": hotkey.KeyReturn,
	"tab":    hotkey.KeyTab,
	"escape": hotkey.KeyEscape,
	"esc":    hotkey.KeyEscape,
	"delete": hotkey.KeyDelete,
	"left":   hotkey.KeyLeft,
	"right":  hotkey.KeyRight,
	"up":     hotkey.KeyUp,
	"down":   hotkey.KeyDown,
	"f1":     hotkey.KeyF1,
	"f2":     hotkey.KeyF2,
	"f3":     hotkey.KeyF3,
	"f4":     hotkey.KeyF4,
	"f5":     hotkey.KeyF5,
	"f6":     hotkey.KeyF6,
	"f7":     hotkey.KeyF7,
	"f8":     hotkey.KeyF8,
	"f9":     hotkey.KeyF9,
	"f10":    hotkey.KeyF10,
	"f11":    hotkey.KeyF11,
	"f12":    hotkey.KeyF12,
	"f13":    hotkey.KeyF13,
	"f14":    hotkey.KeyF14,
	"f15":    hotkey.KeyF15,
	"f16":    hotkey.KeyF16,
	"f17":    hotkey.KeyF17,
	"f18":    hotkey.KeyF18,
	"f19":    hotkey.KeyF19,
	"f20":    hotkey.KeyF20,
}

// ParseHotkey converts string config values to a hotkey object.
func ParseHotkey(modifiers []string, key string) (*hotkey.Hotkey, error) {
	var mods []hotkey.Modifier
	for _, m := range modifiers {
		mod, ok := modMap[m]
		if !ok {
			return nil, fmt.Errorf("unknown modifier: %s (valid: ctrl, shift, mod1-mod5)", m)
		}
		mods = append(mods, mod)
	}
	k, ok := keyMap[key]
	if !ok {
		return nil, fmt.Errorf("unknown key: %s (valid: a-z, 0-9, space, enter, tab, escape, delete, arrows, f1-f20)", key)
	}
	return hotkey.New(mods, k), nil
}
