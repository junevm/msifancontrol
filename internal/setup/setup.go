package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckAndSetup ensures the ec_sys module is loaded with write support.
// If not, it attempts to load it.
// If that fails, it returns an error indicating setup is needed.
func CheckAndSetup() error {
	// 1. Check if module is loaded
	if isModuleLoaded("ec_sys") {
		if checkWriteSupport() {
			return nil // All good
		}
		// Loaded but no write support. Try to reload.
		_ = exec.Command("sudo", "modprobe", "-r", "ec_sys").Run()
		_ = exec.Command("sudo", "modprobe", "ec_sys", "write_support=1").Run()

		if checkWriteSupport() {
			return nil
		}
		return fmt.Errorf("ec_sys loaded but write support refused")
	}

	// 2. Not loaded. Try to load.
	if err := exec.Command("sudo", "modprobe", "ec_sys", "write_support=1").Run(); err == nil {
		if isModuleLoaded("ec_sys") && checkWriteSupport() {
			return nil
		}
	}

	return fmt.Errorf("ec_sys module missing or failed to load")
}

// RunFullSetup performs the full build and install process.
// This should be called if CheckAndSetup fails and the user agrees to build.
func RunFullSetup(progressChan chan<- string) error {
	log := func(format string, a ...interface{}) {
		if progressChan != nil {
			progressChan <- fmt.Sprintf(format, a...)
		} else {
			fmt.Printf(format+"\n", a...)
		}
	}

	if os.Geteuid() != 0 {
		return fmt.Errorf("setup requires root privileges (run with sudo)")
	}

	log("Starting automated build of ec_sys module...")

	// Helper to run command and log output
	runCmd := func(cmd *exec.Cmd) error {
		log("Running: %s %s", filepath.Base(cmd.Path), strings.Join(cmd.Args[1:], " "))
		
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start %s: %v", cmd.Path, err)
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log("%s", scanner.Text())
		}

		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("command failed: %v", err)
		}
		return nil
	}

	run := func(name string, args ...string) error {
		return runCmd(exec.Command(name, args...))
	}

	installDeps := func() error {
		if _, err := exec.LookPath("dnf"); err == nil {
			log("Detected dnf (Fedora/RHEL)...")
			return run("sudo", "dnf", "install", "-y", "dnf-utils", "rpmdevtools", "ncurses-devel", "pesign", "elfutils-libelf-devel", "openssl-devel", "bison", "flex", fmt.Sprintf("kernel-devel-%s", unameR()))
		}
		if _, err := exec.LookPath("apt-get"); err == nil {
			log("Detected apt (Ubuntu/Debian/Zorin)...")
			if err := run("sudo", "apt-get", "update"); err != nil {
				return err
			}
			return run("sudo", "apt-get", "install", "-y", "build-essential", "libncurses-dev", "bison", "flex", "libssl-dev", "libelf-dev", fmt.Sprintf("linux-headers-%s", unameR()))
		}
		return fmt.Errorf("could not find a supported package manager (dnf or apt)")
	}

	// 1. Install tools
	log("1/13 Installing build tools...")
	if err := installDeps(); err != nil {
		return err
	}

	// For Ubuntu/Zorin, if we are just building the module, we can often do it simpler if we have headers.
	if _, err := exec.LookPath("apt-get"); err == nil {
		return runFullSetupUbuntu(log, runCmd)
	}

	// 2. Create temp dir (Fedora/RHEL branch)
	log("2/13 Creating temporary directory...")
	workDir, err := os.MkdirTemp("", "ec_sys_build")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)
	log("Working in %s", workDir)

	// 3. Setup RPM build tree
	log("3/13 Setting up RPM build tree...")
	rpmbuildDir := filepath.Join(workDir, "rpmbuild")
	for _, dir := range []string{"BUILD", "RPMS", "SOURCES", "SPECS", "SRPMS"} {
		if err := os.MkdirAll(filepath.Join(rpmbuildDir, dir), 0755); err != nil {
			return err
		}
	}

	// 4. Download source
	log("4/13 Downloading kernel source...")
	if _, err := exec.LookPath("dnf"); err == nil {
		_ = run("dnf", "config-manager", "--set-enabled", "fedora-source", "updates-source")
		
		cmd := exec.Command("dnf", "download", "--source", fmt.Sprintf("kernel-%s", unameR()))
		cmd.Dir = workDir
		if err := runCmd(cmd); err != nil {
			return fmt.Errorf("failed to download kernel source: %w", err)
		}
	} else {
		return fmt.Errorf("dnf not found. automated kernel source download only supported on Fedora/RHEL")
	}

	// Find SRC RPM
	files, _ := os.ReadDir(workDir)
	var srcRpm string
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "kernel-") && strings.HasSuffix(f.Name(), ".src.rpm") {
			srcRpm = filepath.Join(workDir, f.Name())
			break
		}
	}
	if srcRpm == "" {
		return fmt.Errorf("failed to find downloaded src.rpm")
	}

	// 5. Install build deps
	log("5/13 Installing build dependencies...")
	if err := run("dnf", "builddep", "-y", srcRpm); err != nil {
		return err
	}

	// 6. Install source RPM
	log("6/13 Installing source RPM...")
	if err := run("rpm", "-i", fmt.Sprintf("--define=_topdir %s", rpmbuildDir), srcRpm); err != nil {
		return err
	}

	// 7. Prepare source tree
	log("7/13 Preparing kernel source tree...")
	specsDir := filepath.Join(rpmbuildDir, "SPECS")
	cmd := exec.Command("rpmbuild", "-bp", fmt.Sprintf("--define=_topdir %s", rpmbuildDir), fmt.Sprintf("--target=%s", unameM()), "kernel.spec")
	cmd.Dir = specsDir
	if err := runCmd(cmd); err != nil {
		return fmt.Errorf("failed to prepare kernel source: %w", err)
	}

	// 8. Find build dir
	log("8/13 Locating build directory...")
	buildRoot := filepath.Join(rpmbuildDir, "BUILD")
	var kernelBuildDir string
	_ = filepath.Walk(buildRoot, func(path string, info os.FileInfo, err error) error {
		if kernelBuildDir != "" {
			return filepath.SkipDir
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), "linux-") {
			if _, err := os.Stat(filepath.Join(path, "Makefile")); err == nil {
				kernelBuildDir = path
				return filepath.SkipDir
			}
		}
		return nil
	})

	if kernelBuildDir == "" {
		return fmt.Errorf("could not find kernel build directory")
	}

	// 9. Patch Makefile
	log("9/13 Patching Makefile...")
	fullVersion := unameR()
	parts := strings.SplitN(fullVersion, "-", 2)
	if len(parts) < 2 {
		return fmt.Errorf("unexpected kernel version format: %s", fullVersion)
	}
	extraVersion := "-" + parts[1]

	makefile := filepath.Join(kernelBuildDir, "Makefile")
	replaceInFile(makefile, "^EXTRAVERSION =.*", fmt.Sprintf("EXTRAVERSION = %s", extraVersion))

	// 10. Configure
	log("10/13 Configuring kernel...")
	runInDir := func(dir, name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		return runCmd(cmd)
	}

	if err := runInDir(kernelBuildDir, "cp", fmt.Sprintf("/boot/config-%s", unameR()), ".config"); err != nil {
		return err
	}
	
	f, err := os.OpenFile(filepath.Join(kernelBuildDir, ".config"), os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		if _, err := f.WriteString("\nCONFIG_ACPI_EC_DEBUGFS=m\n"); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}

	// 11. Prepare build
	log("11/13 Preparing build...")
	if err := runInDir(kernelBuildDir, "make", "modules_prepare"); err != nil {
		return err
	}

	symvers := fmt.Sprintf("/usr/src/kernels/%s/Module.symvers", unameR())
	if _, err := os.Stat(symvers); err == nil {
		if err := runInDir(kernelBuildDir, "cp", symvers, "."); err != nil {
			return err
		}
	}

	// 12. Build
	log("12/13 Building module (this may take a while)...")
	cmdBuild := exec.Command("make", "M=drivers/acpi", "modules")
	cmdBuild.Dir = kernelBuildDir
	cmdBuild.Env = append(os.Environ(), "KBUILD_MODPOST_WARN=1")
	if err := runCmd(cmdBuild); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 13. Install
	log("13/13 Installing module...")
	koFile := filepath.Join(kernelBuildDir, "drivers", "acpi", "ec_sys.ko")
	if _, err := os.Stat(koFile); err == nil {
		destDir := fmt.Sprintf("/lib/modules/%s/extra", unameR())
		if err := run("mkdir", "-p", destDir); err != nil {
			return err
		}
		if err := run("cp", koFile, filepath.Join(destDir, "ec_sys.ko")); err != nil {
			return err
		}
		if err := run("depmod", "-a"); err != nil {
			return err
		}
		log("Success! ec_sys.ko installed.")
		return nil
	}
	
	return fmt.Errorf("ec_sys.ko not found after build")
}

func runFullSetupUbuntu(log func(string, ...interface{}), runCmd func(*exec.Cmd) error) error {
	log("Starting Ubuntu-specific build for ec_sys module...")
	
	workDir, err := os.MkdirTemp("", "ec_sys_ubuntu")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	headerDir := fmt.Sprintf("/lib/modules/%s/build", unameR())
	if _, err := os.Stat(headerDir); os.IsNotExist(err) {
		return fmt.Errorf("kernel headers not found. run: sudo apt install linux-headers-%s", unameR())
	}

	log("Preparing source...")
	// We'll download the ec_sys.c from the official kernel source if we can't find it locally.
	// Actually, the easiest way to get the exact ec_sys.c for the current kernel:
	sourceUrl := fmt.Sprintf("https://raw.githubusercontent.com/torvalds/linux/refs/tags/v%s/drivers/acpi/ec_sys.c", strings.Split(unameR(), "-")[0])
	
	log("Downloading ec_sys.c from upstream...")
	if err := runCmd(exec.Command("curl", "-L", sourceUrl, "-o", filepath.Join(workDir, "ec_sys.c"))); err != nil {
		return fmt.Errorf("failed to download ec_sys.c: %v", err)
	}

	makefileContent := fmt.Sprintf("obj-m := ec_sys.o\nall:\n\tmake -C %s M=$(PWD) modules\n", headerDir)
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefileContent), 0644); err != nil {
		return err
	}

	log("Building module...")
	buildCmd := exec.Command("make")
	buildCmd.Dir = workDir
	if err := runCmd(buildCmd); err != nil {
		return fmt.Errorf("module build failed: %v", err)
	}

	log("Installing module...")
	koFile := filepath.Join(workDir, "ec_sys.ko")
	destDir := fmt.Sprintf("/lib/modules/%s/extra", unameR())
	if err := exec.Command("sudo", "mkdir", "-p", destDir).Run(); err != nil {
		return err
	}
	if err := exec.Command("sudo", "cp", koFile, filepath.Join(destDir, "ec_sys.ko")).Run(); err != nil {
		return err
	}
	if err := exec.Command("sudo", "depmod", "-a").Run(); err != nil {
		return err
	}
	if err := exec.Command("sudo", "modprobe", "ec_sys", "write_support=1").Run(); err != nil {
		return err
	}

	log("Success! ec_sys module built and installed for Zorin/Ubuntu.")
	return nil
}

// Helpers

func isModuleLoaded(name string) bool {
	content, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	return strings.Contains(string(content), name)
}

func checkWriteSupport() bool {
	content, err := os.ReadFile("/sys/module/ec_sys/parameters/write_support")
	if err != nil {
		return false
	}
	val := strings.TrimSpace(string(content))
	return val == "Y" || val == "1"
}

func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %v\nOutput:\n%s", name, err, string(output))
	}
	return nil
}

func runQuietInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %v\nOutput:\n%s", name, err, string(output))
	}
	return nil
}

func unameR() string {
	out, _ := exec.Command("uname", "-r").Output()
	return strings.TrimSpace(string(out))
}

func unameM() string {
	out, _ := exec.Command("uname", "-m").Output()
	return strings.TrimSpace(string(out))
}

func replaceInFile(path, pattern, replacement string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "EXTRAVERSION =") {
			lines[i] = replacement
			break
		}
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}
