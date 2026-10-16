// Command bokkie is the single binary for the TLS lending management system.
//
// It is both the server and its own installer: one file copied onto the host
// machine, with no runtime to install alongside it. See docs/plans/00-architecture.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

const usage = `bokkie — TLS Palay & Corn lending management system

usage: bokkie <command> [flags]

commands:
  version      print build version and platform
  run          run the server in the foreground
  install      install as a system service and start it
  uninstall    stop the service and remove it
  backup       write a verified backup of the database now
               (-list, -verify <file>, -to <folder>, -set-folder <folder>)
  update       replace the installed binary with a signed release folder
               (-check to inspect one without changing anything)
  import-customers
               load the customer master from a CSV file
               (-check to see what it would do without writing anything)
  serve        open the station window; closing it stops the station
               (on Windows a native window, which is what double-clicking runs)
  setup        the console menu: a counter-PC shortcut, a check, an update
  doctor       check why the station cannot be reached, and change nothing

run "bokkie <command> -h" for the flags of a single command.
`

func main() {
	// Opened as a program — double-clicked on Windows, Bokkie.app on a Mac —
	// it is the station window; everything else keeps the behaviour scripts
	// and the Makefile rely on. The console menu that used to open on a
	// Windows double-click is still `bokkie setup`.
	if openedAsApp() {
		if err := runStationWindow("", app.Options{}); err != nil {
			winsetup.Report("Bokkie", err.Error())
			fmt.Fprintln(os.Stderr, "bokkie:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "version":
		err = cmdVersion(args)
	case "run":
		err = cmdRun(args)
	case "install":
		err = cmdInstall(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "backup":
		err = cmdBackup(args)
	case "update":
		err = cmdUpdate(args)
	case "import-customers":
		err = cmdImportCustomers(args)
	case "serve":
		err = cmdServe(args)
	case "setup":
		err = runGuidedSetup(args)
	case "doctor":
		err = cmdDoctor(args)
	case "installer-hook":
		err = cmdInstallerHook(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "bokkie: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "bokkie:", err)
		os.Exit(1)
	}
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	short := fs.Bool("short", false, "print only the version string")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *short {
		fmt.Println(buildinfo.Version)
		return nil
	}
	fmt.Println(buildinfo.String())
	return nil
}
