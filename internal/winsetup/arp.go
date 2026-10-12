package winsetup

import "strings"

// KeyPath is where Windows looks for the programs it lists in Add/Remove
// Programs. Under HKLM rather than HKCU because the install is machine-wide:
// the service runs with nobody logged in, so an entry owned by one user's
// profile would describe something that is not theirs.
const KeyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Bokkie`

// Program is how bokkie describes itself to Add/Remove Programs.
type Program struct {
	Name            string
	Version         string
	Publisher       string
	InstallLocation string
	Icon            string

	// UninstallExe and UninstallArgs are kept apart so Values can quote the
	// path and leave the arguments alone. Every path this will ever hold
	// contains a space — C:\Program Files\Bokkie — and Windows reads an
	// unquoted one as C:\Program with arguments, which is a Control Panel
	// entry whose Uninstall button fails with a message nobody can act on.
	UninstallExe  string
	UninstallArgs []string
}

// Values are the registry values Windows reads, as strings.
//
// NoModify and NoRepair are set because neither button would do anything: the
// repair path is the setup window, which is the file the person already has.
func (p Program) Values() map[string]string {
	uninstall := `"` + p.UninstallExe + `"`
	if len(p.UninstallArgs) > 0 {
		uninstall += " " + strings.Join(p.UninstallArgs, " ")
	}
	return map[string]string{
		"DisplayName":     p.Name,
		"DisplayVersion":  p.Version,
		"Publisher":       p.Publisher,
		"InstallLocation": p.InstallLocation,
		"DisplayIcon":     p.Icon,
		"UninstallString": uninstall,
		"NoModify":        "1",
		"NoRepair":        "1",
	}
}
