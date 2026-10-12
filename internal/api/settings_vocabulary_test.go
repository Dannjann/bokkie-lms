package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// The settings screen is generic — it renders whatever settings.All holds — but
// the words around each control are not. A setting added in Go with no label
// here shows its key, and a choice option with no label shows its stored token:
// "monthly-on-balance" in the dropdown a manager picks an interest rule from.
//
// Read out of the TypeScript for the same reason the audit vocabulary is: the
// thing under test is two lists in two languages staying in step, and no
// reviewer catches that reliably.
func TestEverySettingAndChoiceHasWordsOnTheScreen(t *testing.T) {
	labels := tsRecordKeys(t, "settingLabels")
	choices := tsRecordKeys(t, "settingChoiceLabels")
	grouped := tsGroupedKeys(t)

	for _, def := range settings.All {
		if !labels[def.Key] {
			t.Errorf("%s has no label in web/src/api/settings.ts; the screen would "+
				"show the raw key", def.Key)
		}
		if !grouped[def.Key] {
			t.Errorf("%s is in no group in web/src/api/settings.ts; it would fall "+
				"to the bottom of the screen under no heading", def.Key)
		}
		for _, option := range def.Options {
			if !choices[option] {
				t.Errorf("%s may be %q, which has no wording; the dropdown would "+
					"offer a hyphenated identifier", def.Key, option)
			}
		}
	}
}

// And the reverse, which catches the likelier mistake: a label written from
// memory for a key or an option that does not exist. Both of the choice
// vocabularies in the first draft of this screen were invented.
func TestTheSettingsScreenNamesNothingThatDoesNotExist(t *testing.T) {
	keys := map[string]bool{}
	options := map[string]bool{}
	for _, def := range settings.All {
		keys[def.Key] = true
		for _, o := range def.Options {
			options[o] = true
		}
	}

	for key := range tsRecordKeys(t, "settingLabels") {
		if !keys[key] {
			t.Errorf("settingLabels names %q, which is not a setting", key)
		}
	}
	for key := range tsGroupedKeys(t) {
		if !keys[key] {
			t.Errorf("settingGroups names %q, which is not a setting", key)
		}
	}
	for option := range tsRecordKeys(t, "settingChoiceLabels") {
		if !options[option] {
			t.Errorf("settingChoiceLabels names %q, which is not an option of any "+
				"choice setting", option)
		}
	}
}

func settingsClientSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", "api", "settings.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

// A record key, quoted or not — both are valid TypeScript and which one is
// written depends on whether the key happens to be a bare identifier.
var recordKey = regexp.MustCompile(`(?m)^\s*(?:'([^']+)'|([A-Za-z_$][\w$]*)):`)

// tsRecordKeys reads the keys of one `export const <name>: Record<...> = {…}`.
func tsRecordKeys(t *testing.T, name string) map[string]bool {
	t.Helper()
	source := settingsClientSource(t)

	marker := "export const " + name + ": Record<string, string> = {"
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("settings.ts no longer declares %s", name)
	}
	body := source[start+len(marker):]
	end := strings.Index(body, "\n}")
	if end < 0 {
		t.Fatalf("settings.ts: %s is not closed on its own line", name)
	}

	keys := map[string]bool{}
	for _, m := range recordKey.FindAllStringSubmatch(body[:end], -1) {
		if m[1] != "" {
			keys[m[1]] = true
		} else {
			keys[m[2]] = true
		}
	}
	if len(keys) == 0 {
		t.Fatalf("no keys found in %s", name)
	}
	return keys
}

// Every `keys: [ … ]` array in the block, however it is wrapped — the entries
// of a group are sometimes on one line with the label and sometimes not, and
// the test should not dictate which.
var groupArray = regexp.MustCompile(`(?s)keys:\s*\[(.*?)\]`)
var groupKey = regexp.MustCompile(`'([a-z][a-z0-9_.]*)'`)

// tsGroupedKeys reads every key listed in settingGroups' `keys:` arrays.
func tsGroupedKeys(t *testing.T) map[string]bool {
	t.Helper()
	source := settingsClientSource(t)

	start := strings.Index(source, "export const settingGroups")
	if start < 0 {
		t.Fatal("settings.ts no longer declares settingGroups")
	}
	body := source[start:]
	end := strings.Index(body, "\n]")
	if end < 0 {
		t.Fatal("settings.ts: settingGroups is not closed on its own line")
	}

	keys := map[string]bool{}
	for _, array := range groupArray.FindAllStringSubmatch(body[:end], -1) {
		for _, m := range groupKey.FindAllStringSubmatch(array[1], -1) {
			keys[m[1]] = true
		}
	}
	if len(keys) == 0 {
		t.Fatal("no keys found in settingGroups")
	}
	return keys
}
