package ec

import (
	"fmt"
	"os"
)

// EcIoFile is the path to the Embedded Controller (EC) debug file exposed by the Linux kernel.
// The EC is a microcontroller on the motherboard that handles low-level tasks like power management,
// keyboard input, and thermal control (fans).
//
// This file is created by the 'ec_sys' kernel module. Writing to it allows us to send commands
// directly to the EC, bypassing the BIOS or OS defaults.
const EcIoFile = "/sys/kernel/debug/ec/ec0/io"

// Write sends a single byte to a specific memory address in the EC.
//
// Parameters:
//   - byteAddr: The memory offset (address) to write to.
//   - value: The byte value (0-255) to write.
//
// This function opens the EC file, seeks to the correct position, and writes the byte.
// It requires root privileges because it modifies hardware state directly.
func Write(byteAddr int64, value byte) error {
	f, err := os.OpenFile(EcIoFile, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open EC file: %w", err)
	}
	defer f.Close()

	_, err = f.Seek(byteAddr, 0)
	if err != nil {
		return fmt.Errorf("failed to seek to byte %x: %w", byteAddr, err)
	}

	_, err = f.Write([]byte{value})
	if err != nil {
		return fmt.Errorf("failed to write value %d to byte %x: %w", value, byteAddr, err)
	}

	return nil
}

// Read retrieves data from a specific memory address in the EC.
//
// Parameters:
//   - byteAddr: The memory offset (address) to read from.
//   - size: The number of bytes to read (typically 1 or 2).
//
// Returns:
//   - int: The integer value read (if size is 2, it combines bytes as Big Endian).
//   - error: Any error encountered during the operation.
func Read(byteAddr int64, size int) (int, error) {
	f, err := os.OpenFile(EcIoFile, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to open EC file: %w", err)
	}
	defer f.Close()

	_, err = f.Seek(byteAddr, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to seek to byte %x: %w", byteAddr, err)
	}

	buf := make([]byte, size)
	_, err = f.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("failed to read from byte %x: %w", byteAddr, err)
	}

	value := 0
	if size == 1 {
		value = int(buf[0])
	} else if size == 2 {
		// We treat the first byte as the Most Significant Byte (MSB) - Big Endian.
		// Example: [0x01, 0x02] becomes 0x0102 (258 in decimal).
		value = int(buf[0])<<8 | int(buf[1])
	}

	return value, nil
}
