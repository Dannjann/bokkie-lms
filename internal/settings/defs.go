// Package settings is the typed key/value store for everything that must be
// changeable without a rebuild.
//
// The dividing line against config.yaml is deliberate and worth stating once:
// config.yaml holds what must be known *before* the database is open, or what
// is a property of the machine rather than the business — the data directory,
// the port, the log level, the folder backups are written to. Everything a
// person at the station would reasonably want to change — when the backup runs,
// how many to keep, where the Ref. No. sequence starts, how the slip lines up
// in the printer — lives here, where changing it takes effect immediately.
package settings

// Def describes one setting: its key, the type it parses as, and the value a
// fresh install starts with. Defaults live here rather than in the migration so
// the default and the parser that reads it cannot drift apart.
type Def struct {
	Key     string
	Kind    Kind
	Default string
	Doc     string

	// Options is the set a KindChoice setting may hold, in the order a settings
	// screen should offer them. Empty for every other kind.
	Options []string
}

// Kind is the type a setting's stored text parses as. Storing everything as
// TEXT and declaring the type here keeps the table trivial while still making a
// typo — "fourteen" where a number belongs — a loud error at read time instead
// of a silent zero.
type Kind int

const (
	KindString Kind = iota
	KindInt
	KindBool
	KindTimeOfDay // "HH:MM" in the station's timezone
	KindTimezone  // an IANA name, e.g. Asia/Manila
	KindChoice    // one of a fixed list, named in Def.Options
)

func (k Kind) String() string {
	switch k {
	case KindInt:
		return "whole number"
	case KindBool:
		return "true or false"
	case KindTimeOfDay:
		return "time of day as HH:MM"
	case KindTimezone:
		return "IANA timezone name"
	case KindChoice:
		return "one of a fixed list"
	default:
		return "text"
	}
}

// The known settings. Adding one here is all that is needed: it is seeded on
// the next start and readable immediately.
var (
	// Timezone is the station's business timezone. Timestamps are stored in
	// UTC everywhere; this is the only thing that decides what date a
	// transaction is considered to have happened on, which is why it is a
	// setting and not a display preference.
	Timezone = Def{
		Key:     "timezone",
		Kind:    KindTimezone,
		Default: "Asia/Manila",
		Doc:     "Timezone business dates are rendered and totalled in.",
	}

	// BackupSchedule is when the nightly backup runs. 21:00 is after the
	// station closes and long before anyone arrives, so a backup never
	// competes with someone posting a payment.
	BackupSchedule = Def{
		Key:     "backup.schedule",
		Kind:    KindTimeOfDay,
		Default: "21:00",
		Doc:     "Local time the nightly backup runs.",
	}

	// BackupEnabled exists so the nightly job can be turned off for a day of
	// maintenance without editing a file and restarting the service.
	BackupEnabled = Def{
		Key:     "backup.enabled",
		Kind:    KindBool,
		Default: "true",
		Doc:     "Whether the nightly backup runs at all.",
	}

	// BackupRetention is how many backup files to keep. Fourteen daily copies
	// is two weeks of mistakes to notice one in, which is the realistic window
	// for "that balance has looked wrong since last week".
	BackupRetention = Def{
		Key:     "backup.retention",
		Kind:    KindInt,
		Default: "14",
		Doc:     "How many backup files to keep before the oldest is deleted.",
	}

	// OrNoStart is RefNoStart's twin for receipts. They are separate sequences
	// because they are separate pads of paper, and the legacy system's last OR
	// number goes here on the morning of the cutover. Step 6 consumes it.
	OrNoStart = Def{
		Key:     "orno.start",
		Kind:    KindInt,
		Default: "1",
		Doc:     "First OR No. this system issues. Set at cutover to continue the legacy sequence.",
	}

	// PaymentApplies decides which half of a debt a payment clears first when a
	// customer owes both principal and interest.
	//
	// This is Q5 of the domain interview, and it is unanswered. It is a setting
	// rather than a constant so that the answer, when it comes, is a change
	// somebody makes on a screen. Nothing charges interest until step 7, so no
	// payment posted before then can be affected by it either way — which is
	// exactly why settling on a default now costs nothing.
	//
	// It is a choice rather than a string because a typo here changes what
	// money does. "intrest-first" must be refused at the edge, not silently
	// read as the default by the fall-back-to-default rule every other reader
	// relies on.
	PaymentApplies = Def{
		Key:     "payment.applies",
		Kind:    KindChoice,
		Default: "interest-first",
		Options: []string{"interest-first", "principal-first"},
		Doc:     "Which part of a debt a payment clears first when a customer owes both.",
	}

	// RefNoStart is the number the voucher sequence continues from at cutover.
	// The legacy system's last Ref. No. goes here so staff see no
	// discontinuity on the morning they switch. Step 5 consumes it.
	RefNoStart = Def{
		Key:     "refno.start",
		Kind:    KindInt,
		Default: "1",
		Doc:     "First Ref. No. this system issues. Set at cutover to continue the legacy sequence.",
	}
)

// All is every known setting, in the order a settings screen should show them.
// Seeding and the settings API both read this, so there is one list.
var All = []Def{
	Timezone,
	BackupSchedule,
	BackupEnabled,
	BackupRetention,
	RefNoStart,
	OrNoStart,
	PaymentApplies,
}

// byKey indexes All for lookup by the string a request carries.
var byKey = func() map[string]Def {
	m := make(map[string]Def, len(All))
	for _, d := range All {
		m[d.Key] = d
	}
	return m
}()

// Lookup finds a definition by key. An unknown key is rejected rather than
// stored: a settings table that accepts anything becomes a junk drawer, and a
// misspelled key would silently never be read by the code that wanted it.
func Lookup(key string) (Def, bool) {
	d, ok := byKey[key]
	return d, ok
}
