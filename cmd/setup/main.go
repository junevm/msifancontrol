package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// main is the entry point for the setup script.
// Its job is to ensure the 'ec_sys' kernel module is available and has write support enabled.
// Without this module, we cannot control the fans.
func main() {
	// 1. Check if ec_sys is loaded by reading /proc/modules.
	// This works for any user (no root needed) and avoids permission issues with /sys/kernel/debug.
	if isModuleLoaded("ec_sys") {
		fmt.Println("✅ ec_sys module is detected.")

		// Check if write support is enabled.
		if !checkWriteSupport() {
			fmt.Println("⚠️ ec_sys loaded but write support is disabled. Attempting to reload...")
			
			// Try to reload with write support
			_ = exec.Command("sudo", "modprobe", "-r", "ec_sys").Run()
			_ = exec.Command("sudo", "modprobe", "ec_sys", "write_support=1").Run()

			if checkWriteSupport() {
				fmt.Println("✅ ec_sys reloaded with write support.")
				return
			}
			
			fmt.Println("❌ Failed to enable write support. You may need to rebuild manually.")
			// We return here because the module IS present, so building again might not help
			// if the issue is just parameters or permissions.
			return
		}
		
		fmt.Println("✅ ec_sys has write support enabled.")
		return
	}

	// 2. Try to load the module if it's installed but not loaded.
	fmt.Println("Attempting to load ec_sys module...")
	if err := exec.Command("sudo", "modprobe", "ec_sys", "write_support=1").Run(); err == nil {
		if isModuleLoaded("ec_sys") {
			if checkWriteSupport() {
				fmt.Println("✅ ec_sys module loaded successfully.")
				return
			}
		}
	}

	// 3. Check for Root Privileges (or warn about them).
	// Building kernel modules requires installing packages, which needs root.
	if os.Geteuid() != 0 {
		fmt.Println("This script requires root privileges for some operations (dnf install, etc).")
		fmt.Println("However, some parts (rpmdev-setuptree) should run as normal user.")
		fmt.Println("Please run as normal user, sudo will be invoked when necessary.")
	}

	fmt.Println("Detected missing or incomplete ec_sys module. This tool will rebuild the ACPI drivers from Fedora kernel source with EC debugfs enabled.")
	
	// ---------------------------------------------------------
	// AUTOMATED KERNEL MODULE BUILD PROCESS
	// ---------------------------------------------------------
	// On Fedora (and some other distros), the 'ec_sys' module is not enabled by default
	// or is compiled without the ability to enable write support.
	// We have to download the kernel source code, enable the feature, and rebuild the driver.

	// 1. Install necessary build tools and headers.
	// 'dnf-utils': for downloading source RPMs.
	// 'rpmdevtools': for setting up the build environment.
	// 'kernel-devel': headers for the current running kernel.
	runCommand("sudo", "dnf", "install", "-y", "dnf-utils", "rpmdevtools", "ncurses-devel", "pesign", "elfutils-libelf-devel", "openssl-devel", "bison", "flex", fmt.Sprintf("kernel-devel-%s", unameR()))

	// 2. Create a temporary directory for our work to keep things clean.
	workDir, err := os.MkdirTemp("", "ec_sys_build")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(workDir) // Clean up when done.
	fmt.Printf("Working in %s\n", workDir)

	// 3. Setup RPM build tree in the temporary directory.
	// We avoid using the user's home directory (~/rpmbuild) to prevent pollution.
	rpmbuildDir := filepath.Join(workDir, "rpmbuild")
	for _, dir := range []string{"BUILD", "RPMS", "SOURCES", "SPECS", "SRPMS"} {
		if err := os.MkdirAll(filepath.Join(rpmbuildDir, dir), 0755); err != nil {
			fatal(err)
		}
	}

	// 4. Download the kernel source code.
	// We enable the 'fedora-source' repository to find the source code for our kernel.
	// We ignore errors here because it might already be enabled.
	_ = exec.Command("sudo", "dnf", "config-manager", "--set-enabled", "fedora-source", "updates-source").Run()
	
	cmd := exec.Command("dnf", "download", "--source", fmt.Sprintf("kernel-%s", unameR()))
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("failed to download kernel source: %w", err))
	}

	// Find the downloaded .src.rpm file.
	files, _ := os.ReadDir(workDir)
	var srcRpm string
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "kernel-") && strings.HasSuffix(f.Name(), ".src.rpm") {
			srcRpm = filepath.Join(workDir, f.Name())
			break
		}
	}
	if srcRpm == "" {
		fatal(fmt.Errorf("failed to find downloaded src.rpm"))
	}

	// 5. Install build dependencies for the kernel.
	// This ensures we have all the compilers and libraries needed to build the kernel.
	runCommand("sudo", "dnf", "builddep", "-y", srcRpm)

	// 6. Install the source RPM into our custom rpmbuild directory.
	// We use --define "_topdir ..." to tell rpm where to extract.
	runCommand("rpm", "-i", fmt.Sprintf("--define=_topdir %s", rpmbuildDir), srcRpm)

	// 7. Prepare the source tree.
	// This applies Fedora's patches to the vanilla Linux kernel source.
	specsDir := filepath.Join(rpmbuildDir, "SPECS")
	
	cmd = exec.Command("rpmbuild", "-bp", fmt.Sprintf("--define=_topdir %s", rpmbuildDir), fmt.Sprintf("--target=%s", unameM()), "kernel.spec")
	cmd.Dir = specsDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("failed to prepare kernel source: %w", err))
	}

	// 8. Find the directory where the source was extracted.
	// Usually: <rpmbuildDir>/BUILD/kernel-X.Y.Z-build/kernel-X.Y.Z/linux-X.Y.Z
	buildRoot := filepath.Join(rpmbuildDir, "BUILD")
	var kernelBuildDir string
	
	// We ignore the error from Walk because we are just searching.
	// If we don't find it, we check kernelBuildDir below.
	_ = filepath.Walk(buildRoot, func(path string, info os.FileInfo, err error) error {
		if kernelBuildDir != "" {
			return filepath.SkipDir
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), "linux-") {
			// Check if it looks like a kernel source (has a Makefile).
			if _, err := os.Stat(filepath.Join(path, "Makefile")); err == nil {
				kernelBuildDir = path
				return filepath.SkipDir
			}
		}
		return nil
	})

	if kernelBuildDir == "" {
		fatal(fmt.Errorf("could not find kernel build directory in %s", buildRoot))
	}
	fmt.Printf("Found kernel build dir: %s\n", kernelBuildDir)

	// 9. Patch the Makefile.
	// The kernel source usually has a generic version (e.g., 6.8.0).
	// But our running kernel has a specific Fedora version (e.g., 6.8.0-200.fc39).
	// We need to update the 'EXTRAVERSION' in the Makefile so the module we build
	// matches the running kernel exactly.
	fullVersion := unameR()
	// Remove the leading X.Y.Z to get the suffix (e.g., "-200.fc39.x86_64").
	parts := strings.SplitN(fullVersion, "-", 2)
	if len(parts) < 2 {
		fatal(fmt.Errorf("unexpected kernel version format: %s", fullVersion))
	}
	extraVersion := "-" + parts[1]

	makefile := filepath.Join(kernelBuildDir, "Makefile")
	replaceInFile(makefile, "^EXTRAVERSION =.*", fmt.Sprintf("EXTRAVERSION = %s", extraVersion))

	// 10. Configure the kernel build.
	// We copy the configuration of the currently running kernel (.config).
	runCommandInDir(kernelBuildDir, "cp", fmt.Sprintf("/boot/config-%s", unameR()), ".config")
	
	// Enable the specific feature we need: CONFIG_ACPI_EC_DEBUGFS.
	// We append this to the .config file.
	f, err := os.OpenFile(filepath.Join(kernelBuildDir, ".config"), os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		if _, err := f.WriteString("\nCONFIG_ACPI_EC_DEBUGFS=m\n"); err != nil {
			f.Close()
			fatal(err)
		}
		f.Close()
	}

	// 11. Prepare the build environment.
	runCommandInDir(kernelBuildDir, "make", "modules_prepare")

	// Copy Module.symvers to ensure symbol compatibility.
	symvers := fmt.Sprintf("/usr/src/kernels/%s/Module.symvers", unameR())
	if _, err := os.Stat(symvers); err == nil {
		runCommandInDir(kernelBuildDir, "cp", symvers, ".")
	}

	// 12. Build the module!
	// We only build the 'drivers/acpi' directory to save time.
	fmt.Println("Building ACPI modules...")
	cmd = exec.Command("make", "M=drivers/acpi", "modules")
	cmd.Dir = kernelBuildDir
	cmd.Env = append(os.Environ(), "KBUILD_MODPOST_WARN=1") // Ignore some warnings.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("build failed: %w", err))
	}

	// 13. Install the new module.
	koFile := filepath.Join(kernelBuildDir, "drivers", "acpi", "ec_sys.ko")
	if _, err := os.Stat(koFile); err == nil {
		destDir := fmt.Sprintf("/lib/modules/%s/extra", unameR())
		runCommand("sudo", "mkdir", "-p", destDir)
		runCommand("sudo", "cp", koFile, filepath.Join(destDir, "ec_sys.ko"))
		runCommand("sudo", "depmod", "-a") // Update module dependency list.
		fmt.Println("Success! ec_sys.ko installed.")
		fmt.Println("Please run: sudo modprobe ec_sys write_support=1")
	} else {
		fatal(fmt.Errorf("ec_sys.ko not found after build"))
	}
}

// Helper function to run a command and print output.
func runCommand(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("command %s failed: %w", name, err))
	}
}

// Helper function to run a command in a specific directory.
func runCommandInDir(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("command %s failed: %w", name, err))
	}
}

// unameR returns the kernel release (e.g., "6.8.0-200.fc39.x86_64").
func unameR() string {
	out, _ := exec.Command("uname", "-r").Output()
	return strings.TrimSpace(string(out))
}

// unameM returns the machine hardware name (e.g., "x86_64").
func unameM() string {
	out, _ := exec.Command("uname", "-m").Output()
	return strings.TrimSpace(string(out))
}

// fatal prints an error and exits.
func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

// replaceInFile replaces a line starting with 'pattern' with 'replacement'.
func replaceInFile(path, pattern, replacement string) {
	content, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		// Simple prefix check instead of full regex for simplicity.
		// The pattern passed is "^EXTRAVERSION =.*", so we check for "EXTRAVERSION ="
		if strings.HasPrefix(line, "EXTRAVERSION =") {
			lines[i] = replacement
			break
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		fatal(err)
	}
}

// isModuleLoaded checks if a kernel module is loaded by reading /proc/modules.
func isModuleLoaded(name string) bool {
	content, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	return strings.Contains(string(content), name)
}

// checkWriteSupport checks if the ec_sys module has write support enabled.
func checkWriteSupport() bool {
	content, err := os.ReadFile("/sys/module/ec_sys/parameters/write_support")
	if err != nil {
		return false
	}
	val := strings.TrimSpace(string(content))
	return val == "Y" || val == "1"
}

