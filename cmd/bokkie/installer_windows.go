//go:build windows

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// cmdInstallerHook is the part of the Windows installer that needs this
// program's own knowledge. The installer (installer/windows/bokkie.nsi) runs it
// as administrator, without a console of its own, and shows what it prints in
// its details list — so it asks nothing and says everything.
//
//	prepare  before files are copied: remove the service an older Bokkie
//	         registered, which would otherwise hold bokkie.exe open and start
//	         the station on every reboot.
//	finish   after files are copied: let every account on this computer
//	         write a ledger an older Bokkie left in C:\ProgramData\Bokkie.
func cmdInstallerHook(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: bokkie installer-hook prepare|finish")
	}
	switch args[0] {
	case "prepare":
		return removeServiceForInstaller()
	case "finish":
		return grantOldDataFolder()
	}
	return fmt.Errorf("unknown installer step %q", args[0])
}

func removeServiceForInstaller() error {
	// No bootstrap: that would make a data folder for the administrator
	// account approving the prompt, which is not the account that will run
	// the station. The service is found by name; the options only describe it.
	opts := service.Options{Port: defaultPort}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	installed, err := service.IsInstalled(opts)
	if err != nil || !installed {
		fmt.Println("No older Bokkie service on this computer.")
		return nil
	}
	fmt.Println("Removing the older Bokkie service, which started by itself after a restart.")
	if err := service.Uninstall(log, opts); err != nil {
		return fmt.Errorf("the older Bokkie service could not be removed: %w", err)
	}
	fmt.Println("The older Bokkie service has been removed.")
	return nil
}

// usersSID is BUILTIN\Users, by SID so it works on a Windows in any language.
const usersSID = "*S-1-5-32-545"

func grantOldDataFolder() error {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	dir := filepath.Join(base, "Bokkie")
	if _, err := os.Stat(filepath.Join(dir, config.DatabaseName)); err != nil {
		fmt.Println("No records from an older Bokkie in", dir)
		return nil
	}
	// Every account rather than the one running the installer: the person
	// approving the administrator prompt is often not the person who will
	// open Bokkie each morning.
	out, err := exec.Command("icacls", winsetup.GrantArgs(dir, usersSID)...).CombinedOutput()
	if err != nil {
		return winsetup.GrantFailed(dir, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
	}
	fmt.Println("Bokkie's records in", dir, "can now be opened by the people who use this computer.")
	return nil
}
