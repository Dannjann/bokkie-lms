package winsetup

import "fmt"

// DefaultAddress is the host as every counter PC reaches it.
const DefaultAddress = "http://bokkie.local:8080"

// ShortcutScript writes a .lnk through WScript.Shell.
//
// PowerShell rather than COM from Go: it is the same call Create Bokkie
// Shortcut.cmd has always made, it needs no new dependency, and the one thing
// worse than shelling out here would be a cgo-free project growing an OLE
// binding for a single file write.
//
// Edge is launched with --app so staff get something that looks like a desktop
// program rather than a web page they might navigate away from.
func ShortcutScript(edge, address, lnk string) string {
	return fmt.Sprintf(
		`$s = (New-Object -ComObject WScript.Shell).CreateShortcut(%s); `+
			`$s.TargetPath = %s; $s.Arguments = %s; `+
			`$s.Description = 'TLS Palay and Corn lending system'; $s.Save()`,
		quote(lnk), quote(edge), quote("--app="+address))
}

// quote makes a PowerShell single-quoted string. A path with an apostrophe in
// it is rare and an address the operator typed is not, so doubling is not
// optional.
func quote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '\'')
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'')
		}
		out = append(out, r)
	}
	return string(append(out, '\''))
}
