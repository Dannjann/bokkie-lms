package main

// setupState is what the guided window knows about this machine before it
// offers anything.
type setupState struct {
	Installed        bool
	InstalledVersion string
	ThisVersion      string
	Elevated         bool
}

type setupAction int

const (
	actionInstall setupAction = iota
	actionUpdate
	actionRepair
	actionShortcut
	actionUninstall
	actionQuit
)

// setupChoice is one line of the menu.
type setupChoice struct {
	Action     setupAction
	Label      string
	NeedsAdmin bool
}

// menuFor decides what this machine is offered.
//
// The counter PCs are the reason the shortcut is offered first on a machine
// with nothing installed, and the reason it is marked as needing no
// administrator: most people running this file are standing at a counter PC
// that must never become a second host.
func menuFor(state setupState) []setupChoice {
	if !state.Installed {
		return []setupChoice{
			{actionShortcut, "Put a Bokkie shortcut on this computer's desktop", false},
			{actionInstall, "Install Bokkie on this computer (this makes it the host)", true},
			{actionQuit, "Close", false},
		}
	}

	choices := []setupChoice{}
	if state.ThisVersion != state.InstalledVersion {
		choices = append(choices, setupChoice{actionUpdate,
			"Update Bokkie from " + state.InstalledVersion + " to " + state.ThisVersion, true})
	} else {
		choices = append(choices, setupChoice{actionRepair,
			"Repair this installation (reinstall " + state.InstalledVersion + ")", true})
	}
	return append(choices,
		setupChoice{actionShortcut, "Put a Bokkie shortcut on this computer's desktop", false},
		setupChoice{actionUninstall, "Remove Bokkie from this computer (the data folder is kept)", true},
		setupChoice{actionQuit, "Close", false},
	)
}
