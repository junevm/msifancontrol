package fan

import (
	"fmt"

	"github.com/junevm/msifancontrol/internal/config"
	"github.com/junevm/msifancontrol/internal/ec"
)

// ApplyProfile sends the settings from the configuration to the hardware (EC).
// It looks at which profile is selected (Auto, Basic, Advanced, or Cooler Booster)
// and writes the appropriate values to the Embedded Controller's memory.
func ApplyProfile(cfg config.Config) error {
	// These variables hold the memory addresses and values needed to switch modes.
	// They come from the configuration file (config.json).
	
	// Address to switch between Auto and Advanced modes.
	autoAdvAddr := int64(cfg.AutoAdvValues[0])
	// Value to write to enable Auto mode.
	autoVal := byte(cfg.AutoAdvValues[1])
	// Value to write to enable Advanced mode.
	advVal := byte(cfg.AutoAdvValues[2])

	// Address to switch Cooler Booster on or off.
	cbAddr := int64(cfg.CoolerBoosterOffOnValues[0])
	// Value to write to turn Cooler Booster OFF.
	cbOffVal := byte(cfg.CoolerBoosterOffOnValues[1])
	// Value to write to turn Cooler Booster ON.
	cbOnVal := byte(cfg.CoolerBoosterOffOnValues[2])

	switch cfg.Profile {
	case 1: // Auto Mode
		// In Auto mode, the system manages fan speeds automatically based on factory defaults.
		
		// 1. Turn off Cooler Booster (if it was on).
		if err := ec.Write(cbAddr, cbOffVal); err != nil {
			return err
		}
		// 2. Set the mode to "Auto".
		if err := ec.Write(autoAdvAddr, autoVal); err != nil {
			return err
		}
		// 3. Write the specific fan curve points for Auto mode.
		if err := writeSpeeds(cfg.CpuGpuFanSpeedAddress, cfg.AutoSpeed); err != nil {
			return err
		}

	case 2: // Basic Mode
		// Basic mode applies a simple offset (increase or decrease) to the default fan curve.
		
		// 1. Turn off Cooler Booster.
		if err := ec.Write(cbAddr, cbOffVal); err != nil {
			return err
		}
		// 2. Set the mode to "Advanced" (Basic is technically a flat Advanced curve).
		if err := ec.Write(autoAdvAddr, advVal); err != nil {
			return err
		}

		// Calculate the fan speeds based on the "BasicOffset".
		// We clamp the offset between -30 and +30 to prevent unsafe values.
		offset := cfg.BasicOffset
		if offset > 30 {
			offset = 30
		}
		if offset < -30 {
			offset = -30
		}

		// Create a temporary fan curve where every point is just the offset value.
		// This is a simplified interpretation of "Basic" mode.
		basicSpeeds := make([][]int, 2) // 2 rows: CPU and GPU
		for i := 0; i < 2; i++ {
			basicSpeeds[i] = make([]int, 7) // 7 temperature points
			for j := 0; j < 7; j++ {
				val := offset
				// Ensure the value is within the valid range (0-150%).
				if val < 0 {
					val = 0
				}
				if val > 150 {
					val = 150
				}
				basicSpeeds[i][j] = val
			}
		}
		// 3. Write these calculated speeds to the EC.
		if err := writeSpeeds(cfg.CpuGpuFanSpeedAddress, basicSpeeds); err != nil {
			return err
		}

	case 3: // Advanced Mode
		// Advanced mode allows setting a custom fan curve with 7 distinct points for CPU and GPU.
		
		// 1. Turn off Cooler Booster.
		if err := ec.Write(cbAddr, cbOffVal); err != nil {
			return err
		}
		// 2. Set the mode to "Advanced".
		if err := ec.Write(autoAdvAddr, advVal); err != nil {
			return err
		}
		// 3. Write the custom fan curve from the configuration.
		if err := writeSpeeds(cfg.CpuGpuFanSpeedAddress, cfg.AdvSpeed); err != nil {
			return err
		}

	case 4: // Cooler Booster Mode
		// Cooler Booster forces fans to maximum speed immediately.
		
		// 1. Turn ON Cooler Booster.
		if err := ec.Write(cbAddr, cbOnVal); err != nil {
			return err
		}
	
	default:
		return fmt.Errorf("unknown profile: %d", cfg.Profile)
	}

	return nil
}

// writeSpeeds is a helper function that writes a full set of fan curve points to the EC.
//
// Parameters:
//   - addresses: A 2x7 grid of memory addresses (where to write).
//     Row 0 is CPU, Row 1 is GPU.
//   - speeds: A 2x7 grid of fan speed values (what to write).
func writeSpeeds(addresses [][]int, speeds [][]int) error {
	for row := 0; row < 2; row++ { // Loop through CPU (0) and GPU (1)
		for col := 0; col < 7; col++ { // Loop through the 7 temperature points
			addr := int64(addresses[row][col])
			val := byte(speeds[row][col])
			
			// Write the speed value to the specific address.
			if err := ec.Write(addr, val); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetTemps reads the current temperature of the CPU and GPU from the EC.
// Returns CPU temp, GPU temp, and any error.
func GetTemps(cfg config.Config) (int, int, error) {
	// Read CPU temperature (1 byte).
	cpuTemp, err := ec.Read(int64(cfg.CpuGpuTempAddress[0]), 1)
	if err != nil {
		return 0, 0, err
	}
	// Read GPU temperature (1 byte).
	gpuTemp, err := ec.Read(int64(cfg.CpuGpuTempAddress[1]), 1)
	if err != nil {
		return 0, 0, err
	}
	return cpuTemp, gpuTemp, nil
}

// GetRPMs reads the current fan speed (in Revolutions Per Minute) from the EC.
// Returns CPU RPM, GPU RPM, and any error.
func GetRPMs(cfg config.Config) (int, int, error) {
	// RPM values are larger than 255, so they take up 2 bytes of memory.
	
	// Read CPU RPM (2 bytes).
	cpuRpm, err := ec.Read(int64(cfg.CpuGpuRpmAddress[0]), 2)
	if err != nil {
		return 0, 0, err
	}
	// Read GPU RPM (2 bytes).
	gpuRpm, err := ec.Read(int64(cfg.CpuGpuRpmAddress[1]), 2)
	if err != nil {
		return 0, 0, err
	}
	return cpuRpm, gpuRpm, nil
}
