// Package ondevice is the backend of the build of reManager that runs on the tablet itself.
package ondevice

import (
	"fmt"
	"os"
	"runtime"

	rmdevice "github.com/rmitchellscott/remarkable-go/device"

	"reManager/internal/commands"
)

// Device is the tablet the backend is running on.
type Device struct {
	Type rmdevice.Type
	Arch rmdevice.Architecture
}

// Models the on-device build has been developed against. Adding a model here is a claim that it was tried on that hardware.
var testedModels = map[rmdevice.Type]bool{
	rmdevice.RMPPMove: true,
}

func (d Device) Tested() bool {
	return testedModels[d.Type]
}

// PackageArch is the architecture name Vellum's catalogue uses.
func (d Device) PackageArch() string {
	return commands.GetDownloadArch(d.Arch)
}

func DetectDevice() (Device, error) {
	model, err := rmdevice.Detect(localFS{})
	if err != nil {
		return Device{}, fmt.Errorf("could not read the tablet model: %w", err)
	}
	if model == rmdevice.Unknown {
		return Device{}, fmt.Errorf("this does not look like a reMarkable tablet")
	}
	var arch rmdevice.Architecture
	switch runtime.GOARCH {
	case "arm64":
		arch = rmdevice.Aarch64
	case "arm":
		arch = rmdevice.Arm32
	default:
		return Device{}, fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	return Device{Type: model, Arch: arch}, nil
}

// localFS gives remarkable-go's detection code the tablet's own filesystem.
type localFS struct{}

func (localFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (localFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (localFS) Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func (localFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (localFS) Remove(path string) error                     { return os.Remove(path) }
func (localFS) RemoveAll(path string) error                  { return os.RemoveAll(path) }
