package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"

	"golang.org/x/term"
)

//go:embed dockerfiles/*
var dockerFiles embed.FS

var distroPath = map[string]string{
	"ubuntu": "docker.io/library/ubuntu",
	"arch":   "docker.io/archlinux/archlinux",
	"fedora": "docker.io/library/fedora:43",
	"debian": "docker.io/library/debian:trixie",
	"alpine": "docker.io/library/alpine:latest",
}

// writeEmbeddedFiles extracts the embedded dockerfiles/ to a temp directory,
// flattening the dockerfiles/ prefix so the build context is flat.
func writeEmbeddedFiles() (string, error) {
	tmpDir, err := os.MkdirTemp("", "linuxformac-build-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	err = fs.WalkDir(dockerFiles, "dockerfiles", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := dockerFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		// Flatten: strip the "dockerfiles/" prefix
		name := filepath.Base(path)
		dest := filepath.Join(tmpDir, name)
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		return nil
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extract embedded files: %w", err)
	}

	return tmpDir, nil
}

// buildImage builds (or reuses) a custom image for the given distro.
// Returns the image tag.
func buildImage(containerRuntime, distro string) (string, error) {
	imageTag := "linuxformac-" + distro

	// Check if the image already exists
	inspectCmd := exec.Command(containerRuntime, "image", "inspect", imageTag)
	if err := inspectCmd.Run(); err == nil {
		log.Printf("Image %s already exists, reusing.", imageTag)
		return imageTag, nil
	}

	log.Printf("Building custom image %s...", imageTag)

	buildCtx, err := writeEmbeddedFiles()
	if err != nil {
		return "", fmt.Errorf("write build context: %w", err)
	}
	defer os.RemoveAll(buildCtx)

	dockerfile := "Dockerfile." + distro
	buildCmd := exec.Command(containerRuntime, "build", "-t", imageTag, "-f", filepath.Join(buildCtx, dockerfile), buildCtx)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("build image %s: %w", imageTag, err)
	}

	log.Printf("Image %s built successfully.", imageTag)
	return imageTag, nil
}

func initializeVM(distro string, testMode bool) error {
	switch runtime.GOOS {
	case "linux":
		if !testMode {
			log.Fatal("Operating System: Linux. Pass --test to run.")
		}
		log.Println("Operating system: ", runtime.GOOS)
		log.Println("Architecture: ", runtime.GOARCH)
	case "windows":
		log.Fatal("Operating System: Windows. Use WSLv2.")
	case "darwin":
		log.Println("Operating system: ", runtime.GOOS)
		log.Println("Architecture: ", runtime.GOARCH)
	}

	// Validate distro
	if _, ok := distroPath[distro]; !ok {
		log.Fatalf("unknown distro %q (supported: ubuntu, arch, fedora, debian, alpine)", distro)
	}

	systemContainer := []string{"podman", "docker"}
	log.Println("Checking system for container software....")

	var present []string
	for _, container := range systemContainer {
		command := fmt.Sprintf("which %s", container)
		cmd := exec.Command("bash", "-c", command)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("%s not found: %v", container, err)
			continue
		}
		log.Println(string(out))
		present = append(present, container)
	}

	if len(present) == 0 {
		log.Fatal("No container runtime found (podman or docker). Install a container tool.")
	}

	containerRuntime := present[0]

	// Build custom image (pulls base image automatically)
	log.Println("Initializing", distro)
	customImageTag, err := buildImage(containerRuntime, distro)
	if err != nil {
		log.Fatalf("Failed to build custom image: %v", err)
	}

	log.Println("Attempting to start VM....")
	log.Println("Running container in Interactive Mode.")

	// Get host user info
	currentUser, err := user.Current()
	if err != nil {
		log.Fatalf("Failed to get current user: %v", err)
	}
	username := currentUser.Username
	uid := currentUser.Uid
	gid := currentUser.Gid

	// Validate UID/GID are numeric (they should be on Unix)
	if _, err := strconv.Atoi(uid); err != nil {
		log.Fatalf("Non-numeric UID %q: %v", uid, err)
	}
	if _, err := strconv.Atoi(gid); err != nil {
		log.Fatalf("Non-numeric GID %q: %v", gid, err)
	}

	volName, volErr := CreatePersistentVolume(distro)

	args := []string{"run", "-it", "--rm", "--hostname", distro,
		"-e", "HOST_USER=" + username,
		"-e", "HOST_UID=" + uid,
		"-e", "HOST_GID=" + gid,
		"-e", "DISTRO_TYPE=" + distro,
	}

	if volErr != nil {
		log.Println("Cannot create volume. Skipping")
	} else {
		log.Printf("Attaching volume: %s to %s", volName, customImageTag)
		args = append(args, "-v", volName+":/data")
	}

	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err == nil && username != "" {
			args = append(args, "-v", home+":/home/"+username)
		}
	}

	args = append(args, customImageTag)
	runCmd := exec.Command(containerRuntime, args...)
	runCmd.Stdin = os.Stdin
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	err = runCmd.Run()
	if err != nil {
		log.Fatalf("Failed to run VM due to error: %v", err)
	}
	return nil
}

var distroList = []string{"ubuntu", "debian", "arch", "fedora", "alpine"}

// selectDistro presents an interactive arrow-key menu and returns the chosen distro.
func selectDistro() (string, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("enable raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	selected := 0
	buf := make([]byte, 3)

	render := func() {
		// Move cursor to start and clear from here down
		fmt.Print("\r\033[J")
		fmt.Print("Select a Linux distribution:\r\n\r\n")
		for i, d := range distroList {
			if i == selected {
				fmt.Printf("  \033[1;36m> %s\033[0m\r\n", d)
			} else {
				fmt.Printf("    %s\r\n", d)
			}
		}
		fmt.Print("\r\nUse arrow keys to navigate, Enter to select, q to quit.\r\n")
	}

	// Initial render — move up to overwrite on re-render
	render()

	for {
		// Move cursor back up to top of menu for next render
		lines := len(distroList) + 4 // header + blank + items + blank + help
		fmt.Printf("\033[%dA", lines)

		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", fmt.Errorf("read input: %w", err)
		}

		if n == 1 {
			switch buf[0] {
			case 'q', 'Q':
				// Clear the menu before exiting
				fmt.Print("\r\033[J")
				return "", fmt.Errorf("cancelled")
			case 13: // Enter
				fmt.Print("\r\033[J")
				return distroList[selected], nil
			case 'k', 'K': // vim up
				if selected > 0 {
					selected--
				}
			case 'j', 'J': // vim down
				if selected < len(distroList)-1 {
					selected++
				}
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A': // Up arrow
				if selected > 0 {
					selected--
				}
			case 'B': // Down arrow
				if selected < len(distroList)-1 {
					selected++
				}
			}
		}

		render()
	}
}

func CreatePersistentVolume(distro string) (string, error) {
	volumeName := fmt.Sprintf("%s_Volume", distro)

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	path := home + "/" + volumeName
	log.Println("Volume path:", path)

	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("volume path exists but is not a directory: %s", path)
		}
		return path, nil
	}

	if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat volume path: %w", err)
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("create volume dir: %w", err)
	}
	return path, nil
}

func main() {
	var linuxDistro string
	testMode := false

	if len(os.Args) < 2 {
		// Interactive selector — implicitly allows Linux testing
		testMode = true
		choice, err := selectDistro()
		if err != nil {
			log.Fatalf("Distro selection: %v", err)
		}
		linuxDistro = choice
	} else {
		linuxDistro = os.Args[1]
		// Check for --test flag anywhere in remaining args
		for _, arg := range os.Args[2:] {
			if arg == "--test" {
				testMode = true
				break
			}
		}
	}

	fmt.Println("Linux Distro:", linuxDistro)
	if err := initializeVM(linuxDistro, testMode); err != nil {
		log.Printf("Error: %v", err)
	}
}
