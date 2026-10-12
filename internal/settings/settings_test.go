package settings

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
)

func TestSettingsAreSeededOnFirstRun(t *testing.T) {
	store := openStore(t, openTestDB(t))

	for _, def := range All {
		if got := store.Raw(def); got != def.Default {
			t.Errorf("%s = %q on a fresh install, want the default %q", def.Key, got, def.Default)
		}
	}

	if store.Location().String() != "Asia/Manila" {
		t.Errorf("timezone = %q, want Asia/Manila", store.Location())
	}
	if store.Int(BackupRetention) != 14 {
		t.Errorf("backup retention = %d, want 14", store.Int(BackupRetention))
	}
	if !store.Bool(BackupEnabled) {
		t.Error("the nightly backup is off on a fresh install")
	}
	if hour, minute := store.TimeOfDay(BackupSchedule); hour != 21 || minute != 0 {
		t.Errorf("backup schedule = %02d:%02d, want 21:00", hour, minute)
	}
}

// Adding a setting in a later version must fill itself in on the next start —
// otherwise introducing one means writing a migration as well, and someone will
// skip that step.
func TestSeedingFillsInSettingsAnEarlierVersionDidNotKnow(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)

	// Simulate an older install: the table exists but this key was never there.
	openStore(t, database)
	if _, err := database.Exec(`DELETE FROM settings WHERE key = ?`, BackupRetention.Key); err != nil {
		t.Fatalf("remove setting: %v", err)
	}

	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Raw(BackupRetention); got != BackupRetention.Default {
		t.Errorf("%s = %q after reopening, want it seeded to %q", BackupRetention.Key, got, BackupRetention.Default)
	}
}

// A value someone has already changed must survive an upgrade. Reseeding over
// it would silently undo a deliberate decision.
func TestSeedingDoesNotOverwriteAChangedValue(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)

	store := openStore(t, database)
	if err := store.Set(ctx, BackupRetention, "30"); err != nil {
		t.Fatalf("set: %v", err)
	}

	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Int(BackupRetention); got != 30 {
		t.Errorf("retention = %d after a restart, want the 30 someone chose", got)
	}
}

// The whole point of this table: a change takes effect now, not at the next
// restart — which on this machine may be months away.
func TestASetValueIsVisibleImmediately(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, openTestDB(t))

	if err := store.Set(ctx, Timezone, "UTC"); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	if got := store.Location().String(); got != "UTC" {
		t.Errorf("Location() = %q immediately after the change, want UTC", got)
	}

	if err := store.Set(ctx, BackupSchedule, "02:30"); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
	if hour, minute := store.TimeOfDay(BackupSchedule); hour != 2 || minute != 30 {
		t.Errorf("schedule = %02d:%02d, want 02:30", hour, minute)
	}
}

func TestInvalidValuesAreRefused(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, openTestDB(t))

	bad := []struct {
		name  string
		def   Def
		value string
	}{
		{"retention is not a number", BackupRetention, "fourteen"},
		{"retention is zero", BackupRetention, "0"},
		{"retention is negative", BackupRetention, "-1"},
		{"schedule is not a time", BackupSchedule, "9pm"},
		{"schedule hour is out of range", BackupSchedule, "25:00"},
		{"timezone is invented", Timezone, "Mars/Olympus"},
		{"enabled is not a boolean", BackupEnabled, "yes please"},
		{"the application order is misspelled", PaymentApplies, "intrest-first"},
		{"the application order is invented", PaymentApplies, "oldest-voucher-first"},
		{"the application order is empty", PaymentApplies, ""},
		{"the OR sequence starts at zero", OrNoStart, "0"},
	}

	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Set(ctx, tc.def, tc.value); err == nil {
				t.Errorf("%s = %q was accepted", tc.def.Key, tc.value)
			}
			// And the stored value is untouched.
			if got := store.Raw(tc.def); got != tc.def.Default {
				t.Errorf("%s = %q after a refused write, want the default %q", tc.def.Key, got, tc.def.Default)
			}
		})
	}
}

// A choice setting is the one place where falling back to the default is not
// enough on its own: the refusal has to name the legal values, because the
// person reading it is standing at a settings screen having just typed one.
func TestARefusedChoiceSaysWhatIsAllowed(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, openTestDB(t))

	err := store.Set(ctx, PaymentApplies, "intrest-first")
	if err == nil {
		t.Fatal("a misspelled application order was accepted")
	}
	for _, option := range PaymentApplies.Options {
		if !strings.Contains(err.Error(), option) {
			t.Errorf("the refusal %q does not mention %q", err, option)
		}
	}
}

func TestBothApplicationOrdersAreAccepted(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, openTestDB(t))

	for _, option := range PaymentApplies.Options {
		if err := store.Set(ctx, PaymentApplies, option); err != nil {
			t.Fatalf("set %s = %q: %v", PaymentApplies.Key, option, err)
		}
		if got := store.Choice(PaymentApplies); got != option {
			t.Errorf("%s = %q after setting it to %q", PaymentApplies.Key, got, option)
		}
	}
}

// The same hand-edit discipline as every other reader, on the setting where it
// matters most: a nonsense value must mean the default, not an empty string
// that matches neither branch of the split.
func TestAHandEditedChoiceFallsBackToTheDefault(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	store := openStore(t, database)

	if _, err := database.Exec(`UPDATE settings SET value = 'whatever' WHERE key = ?`, PaymentApplies.Key); err != nil {
		t.Fatalf("corrupt setting: %v", err)
	}
	if err := store.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := store.Choice(PaymentApplies); got != PaymentApplies.Default {
		t.Errorf("%s = %q, want the default %q", PaymentApplies.Key, got, PaymentApplies.Default)
	}
}

// Every choice setting must be able to hold its own default, and must offer at
// least two things to choose between. Both are the sort of mistake that is
// invisible until somebody opens the settings screen.
func TestEveryChoiceSettingIsCoherent(t *testing.T) {
	for _, def := range All {
		if def.Kind != KindChoice {
			if len(def.Options) > 0 {
				t.Errorf("%s is not a choice but lists options", def.Key)
			}
			continue
		}
		if len(def.Options) < 2 {
			t.Errorf("%s is a choice between %d things", def.Key, len(def.Options))
		}
		if err := Validate(def, def.Default); err != nil {
			t.Errorf("%s cannot hold its own default: %v", def.Key, err)
		}
	}
}

func TestLookupRefusesAnUnknownKey(t *testing.T) {
	if _, ok := Lookup("backup.retenshun"); ok {
		t.Error("a misspelled key was found")
	}
	if def, ok := Lookup(BackupRetention.Key); !ok || def.Key != BackupRetention.Key {
		t.Error("a real key was not found")
	}
}

// Every reader falls back to the default rather than erroring, so a row edited
// by hand in a sqlite3 shell cannot stop the station from running.
func TestAHandEditedRowFallsBackToTheDefault(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	store := openStore(t, database)

	if _, err := database.Exec(`UPDATE settings SET value = 'fourteen' WHERE key = ?`, BackupRetention.Key); err != nil {
		t.Fatalf("corrupt setting: %v", err)
	}
	if err := store.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := store.Int(BackupRetention); got != 14 {
		t.Errorf("retention = %d, want the default 14 rather than a zero", got)
	}
}

func openStore(t *testing.T, database *sql.DB) *Store {
	t.Helper()
	store, err := Open(context.Background(), database)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	return store
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, filepath.Join(t.TempDir(), "bokkie.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := bokkiedb.Migrate(ctx, database, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}
