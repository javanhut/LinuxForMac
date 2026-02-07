package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
)

var distroPath = map[string]string{
	"ubuntu": "ubuntu",
	"arch":   "base/arch",
	"fedora": "fedora:43",
	"debian": "debian:trixie",
	"alpine": "alpine:latest",
}

func initializeVM(distro string) error {
	switch runtime.GOOS {
	case "linux":
		log.Fatal("Operating System: Linux. Doing nothing.")
	case "windows":
		log.Fatal("Operating System: Windows. Use WSLv2.")
	case "darwin":
		log.Println("Operating system: ", runtime.GOOS)
		log.Println("Architecture: ", runtime.GOARCH)
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

	log.Println("Initializing ", distro)
	image, ok := distroPath[distro]
	if !ok {
		log.Fatalf("unknown distro %q (supported: ubuntu, arch, fedora, debian, alpine)", distro)
	}
	log.Println(fmt.Sprintf("Pulling %s using %s", image, present[0]))
	pullCmd := exec.Command(present[0], "pull", image)
	out, err := pullCmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to pull image %s due to %v", image, err)
	}
	log.Println(string(out))
	log.Println("Finished.")
	log.Println("Attempting to start VM....")
	log.Println("Running container in Interactive Mode.")
	volName, e := CreatePersistentVolume(image)
	var runCmd *exec.Cmd
	if e != nil {
		log.Println("Cannot create volume.Skipping")
		runCmd = exec.Command(present[0], "run", "-it", image, "/bin/bash")
	} else {
		log.Println(fmt.Sprintf("Attaching volume: %s to %s", volName, image))
		runCmd = exec.Command(present[0], "run", "-it", "-v", volName+":/data", image, "/bin/bash")
	}
	runCmd.Stdin = os.Stdin
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	err = runCmd.Run()
	if err != nil {
		log.Fatalf("Failed to run VM due to error: %v", err)
	}
	return nil
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
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s <ubuntu|arch|fedora|debian|alpine>", os.Args[0])
	}
	linuxDistro = os.Args[1]
	if linuxDistro == "" {
		linuxDistro = "No Distro Detected. Input a distro"
		fmt.Println(linuxDistro)
	} else {

		fmt.Println("Linux Distro: ", linuxDistro)
		err := initializeVM(linuxDistro)
		if err != nil {
			log.Printf("Error: %v", err)
		}
	}
}
