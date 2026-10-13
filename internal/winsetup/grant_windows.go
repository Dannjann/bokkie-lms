//go:build windows

package winsetup

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Grant gives the account running this process full control of dir and
// everything in it.
//
// It needs administrator rights, which the caller has already obtained — UAC
// elevation keeps the same user, so the account being granted is the one who
// will open the station afterwards, not the administrator who approved it.
func Grant(dir string) error {
	account, err := currentAccount()
	if err != nil {
		return GrantFailed(dir, err)
	}

	out, err := exec.Command("icacls", GrantArgs(dir, account)...).CombinedOutput()
	if err != nil {
		return GrantFailed(dir, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
	}
	return nil
}

// currentAccount is DOMAIN\user, which is what icacls wants and what
// distinguishes two people with the same name on a machine that has both.
func currentAccount() (string, error) {
	user := os.Getenv("USERNAME")
	if user == "" {
		return "", fmt.Errorf("%%USERNAME%% is not set, so there is no account to give permission to")
	}
	if domain := os.Getenv("USERDOMAIN"); domain != "" {
		return domain + `\` + user, nil
	}
	return user, nil
}
