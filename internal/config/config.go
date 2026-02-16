package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	jsonParser "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// Global koanf instance. Use "." as the key delimiter.
var k = koanf.New(".")

// Config holds the application configuration.
// It defines how the fan control behaves, including profiles, speed curves, and hardware addresses.
//
// The struct tags `koanf` are used by the configuration loader to map JSON keys to struct fields.
// The `json` tags are used when saving the configuration back to disk.
type Config struct {
	// Profile determines the active fan control mode.
	// 1: Auto (System default + curve)
	// 2: Basic (Simple offset applied to default curve)
	// 3: Advanced (Custom user-defined curve)
	// 4: Cooler Booster (Max speed)
	Profile int `koanf:"PROFILE" json:"PROFILE"`

	// AutoSpeed defines the fan speed curve for "Auto" mode.
	// It is a 2D array: [0] is CPU, [1] is GPU.
	// Each array contains 7 integer values representing fan speeds (0-150%) at specific temperature points.
	AutoSpeed [][]int `koanf:"AUTO_SPEED" json:"AUTO_SPEED"`

	// AdvSpeed defines the fan speed curve for "Advanced" mode.
	// Similar structure to AutoSpeed, but used when Profile is set to 3.
	AdvSpeed [][]int `koanf:"ADV_SPEED" json:"ADV_SPEED"`

	// BasicOffset is a value added to the default fan speed in "Basic" mode.
	// Range: -30 to +30. Allows simple "faster" or "slower" adjustments.
	BasicOffset int `koanf:"BASIC_OFFSET" json:"BASIC_OFFSET"`

	// CPU seems to be a flag or identifier for CPU control.
	// In the original logic, it's present but its specific usage might be legacy.
	CPU int `koanf:"CPU" json:"CPU"`

	// AutoAdvValues contains EC (Embedded Controller) addresses and values for switching modes.
	// [0]: Address to write to for mode switching.
	// [1]: Value to write for "Auto" mode.
	// [2]: Value to write for "Advanced" mode.
	AutoAdvValues []int `koanf:"AUTO_ADV_VALUES" json:"AUTO_ADV_VALUES"`

	// CoolerBoosterOffOnValues contains EC addresses and values for Cooler Booster.
	// [0]: Address to write to.
	// [1]: Value for "Off".
	// [2]: Value for "On".
	CoolerBoosterOffOnValues []int `koanf:"COOLER_BOOSTER_OFF_ON_VALUES" json:"COOLER_BOOSTER_OFF_ON_VALUES"`

	// CpuGpuFanSpeedAddress maps the 7 curve points to specific EC memory addresses.
	// [0]: Array of 7 addresses for CPU fan curve points.
	// [1]: Array of 7 addresses for GPU fan curve points.
	CpuGpuFanSpeedAddress [][]int `koanf:"CPU_GPU_FAN_SPEED_ADDRESS" json:"CPU_GPU_FAN_SPEED_ADDRESS"`

	// CpuGpuTempAddress contains the EC addresses to read current temperatures.
	// [0]: CPU Temperature address.
	// [1]: GPU Temperature address.
	CpuGpuTempAddress []int `koanf:"CPU_GPU_TEMP_ADDRESS" json:"CPU_GPU_TEMP_ADDRESS"`

	// CpuGpuRpmAddress contains the EC addresses to read current Fan RPM.
	// [0]: CPU RPM address.
	// [1]: GPU RPM address.
	CpuGpuRpmAddress []int `koanf:"CPU_GPU_RPM_ADDRESS" json:"CPU_GPU_RPM_ADDRESS"`

	// BatteryThresholdValue is likely used for battery charge limiting (not fully implemented in this port yet).
	BatteryThresholdValue int `koanf:"BATTERY_THRESHOLD_VALUE" json:"BATTERY_THRESHOLD_VALUE"`
}

// DefaultConfig returns the hardcoded default configuration.
// These values are specific to MSI laptops (tested on GF65 Thin 9SD).
//
// ⚠️ WARNING: Writing incorrect values to the EC can cause hardware instability.
// Only change these if you know what you are doing or have a different supported model.
func DefaultConfig() Config {
	return Config{
		Profile: 1,
		AutoSpeed: [][]int{
			{0, 40, 48, 56, 64, 72, 80},
			{0, 48, 56, 64, 72, 79, 86},
		},
		AdvSpeed: [][]int{
			{0, 40, 48, 56, 64, 72, 80},
			{0, 48, 56, 64, 72, 79, 86},
		},
		BasicOffset:              0,
		CPU:                      1,
		AutoAdvValues:            []int{0xd4, 13, 141},
		CoolerBoosterOffOnValues: []int{0x98, 2, 130},
		CpuGpuFanSpeedAddress: [][]int{
			{0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78},
			{0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f, 0x90},
		},
		CpuGpuTempAddress:     []int{0x68, 0x80},
		CpuGpuRpmAddress:      []int{0xc8, 0xca},
		BatteryThresholdValue: 100,
	}
}

// GetConfigDir returns the directory where the configuration file is stored.
// Usually ~/.config/MSIFanControl
func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "MSIFanControl"), nil
}

// Load reads the configuration from disk, falling back to defaults if necessary.
// It uses the 'koanf' library to merge default values with the file on disk.
func Load() (Config, error) {
	// 1. Load Defaults
	if err := k.Load(structs.Provider(DefaultConfig(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("error loading default config: %w", err)
	}

	// 2. Load from File
	dir, err := GetConfigDir()
	if err != nil {
		return Config{}, err
	}
	path := filepath.Join(dir, "config.json")

	// If file exists, load it
	if _, err := os.Stat(path); err == nil {
		if err := k.Load(file.Provider(path), jsonParser.Parser()); err != nil {
			return Config{}, fmt.Errorf("error loading config file: %w", err)
		}
	}

	// 3. Unmarshal into struct
	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("error unmarshalling config: %w", err)
	}

	return cfg, nil
}

// Save writes the current configuration to disk.
// It uses standard JSON marshalling to ensure the file is human-readable.
func Save(cfg Config) error {
	dir, err := GetConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// We use standard json marshal here because koanf is primarily for reading/merging.
	// Writing back is often simpler with the standard library if we just want to dump the struct.
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}
