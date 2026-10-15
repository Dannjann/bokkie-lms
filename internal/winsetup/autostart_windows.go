//go:build windows

package winsetup

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// runKeys are the two registry locations Windows starts things from at logon.
const (
	runKeyPath     = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	runOnceKeyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\RunOnce`
	valueName      = "Bokkie"
	taskName       = "Bokkie"
)

// AutoStartMechanisms looks in every place Windows can start something from
// and reports which of them currently name bokkie.
//
// The service is checked by the caller, which owns service.Options; it is
// filled in by serviceAutoStart below so that this function stays a pure
// lookup of the four Windows locations.
func AutoStartMechanisms() []AutoStart {
	return []AutoStart{
		{Name: "service", Where: "the bokkie Windows service", Present: serviceRegistered()},
		{Name: "run key", Where: `HKLM\` + runKeyPath + `\` + valueName, Present: registryValueExists(runKeyPath)},
		{Name: "run once key", Where: `HKLM\` + runOnceKeyPath + `\` + valueName, Present: registryValueExists(runOnceKeyPath)},
		{Name: "startup folder", Where: startupShortcut(), Present: fileExists(startupShortcut())},
		{Name: "scheduled task", Where: `schtasks /query /tn ` + taskName, Present: scheduledTaskExists()},
	}
}

func registryValueExists(path string) bool {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	_, _, err = key.GetStringValue(valueName)
	return err == nil
}

func startupShortcut() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "StartUp", "Bokkie.lnk")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func scheduledTaskExists() bool {
	return exec.Command("schtasks", "/query", "/tn", taskName).Run() == nil
}

// serviceRegistered asks the service manager directly rather than going
// through internal/service, which would import this package's caller.
func serviceRegistered() bool {
	return exec.Command("sc", "query", "bokkie").Run() == nil
}
