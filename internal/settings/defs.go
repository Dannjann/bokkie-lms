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

// String is the phrase that goes in an error message a person reads. It is
// prose and it is allowed to change; nothing should branch on it.
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

// Slug is the same kind as a stable token, for code that has to branch on it —
// the settings screen picking which control to render, above all.
//
// It exists because String returns prose. A screen keyed off "one of a fixed
// list" would break the day somebody improves that sentence, and the break
// would be a settings form silently falling back to a text box for every
// choice setting, which is not a failure anyone would notice quickly.
func (k Kind) Slug() string {
	switch k {
	case KindInt:
		return "int"
	case KindBool:
		return "bool"
	case KindTimeOfDay:
		return "time_of_day"
	case KindTimezone:
		return "timezone"
	case KindChoice:
		return "choice"
	default:
		return "string"
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

	// LANAccess decides whether the other machines on the station network may
	// reach this app, or only the host itself.
	//
	// It is a setting rather than a config.yaml key because it is the one
	// network choice the person at the station makes rather than the person who
	// installed the machine. "Let the counter PCs in" and "just this machine
	// while I sort something out" are both ordinary Tuesday answers, and moving
	// between them must not mean editing a file and restarting a service.
	//
	// Turning it off does not close the port. The listener stays on 0.0.0.0 and
	// requests from anywhere else are refused with an explanation — see
	// internal/api/network.go, which says plainly what that is and is not.
	LANAccess = Def{
		Key:     "network.lan_access",
		Kind:    KindBool,
		Default: "true",
		Doc:     "Whether the other machines on the station network may reach this app, or only the host itself.",
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

	// BackupRetention is how many backup files to keep.
	//
	// It was fourteen, for fourteen daily copies: two weeks of mistakes to
	// notice one in, which is the realistic window for "that balance has looked
	// wrong since last week". Backups stopped being daily in 2.7 — the station
	// runs while a person has its window open, so one is taken when it opens
	// and another when it closes, and a day of ordinary use produces two. A
	// day with a power cut in it produces four or six.
	//
	// Forty keeps the two weeks the number was chosen for. Counting files
	// rather than days is what makes this necessary, and it stays that way on
	// purpose: the folder is often a USB stick, and a count is something a
	// person can look at and verify by eye.
	BackupRetention = Def{
		Key:     "backup.retention",
		Kind:    KindInt,
		Default: "40",
		Doc:     "How many backup files to keep before the oldest is deleted.",
	}

	// OrNoStart is RefNoStart's twin for payments. The legacy system never
	// numbered payments at all, so there is no sequence to continue: if the
	// station keeps a paper O.R. pad, its next number goes here at cutover,
	// and otherwise 1 is right.
	OrNoStart = Def{
		Key:     "orno.start",
		Kind:    KindInt,
		Default: "1",
		Doc:     "First OR No. this system issues. The legacy system had none — set it only if a paper O.R. pad is in use.",
	}

	// PaymentApplies decides which half of a debt a payment clears first when a
	// customer owes both principal and interest.
	//
	// This is Q5 of the domain interview. The legacy system cleared interest
	// first in all 1,097 of its settlements, which is the default; it stays a
	// setting so that the owner's answer, if it differs, is a change somebody
	// makes on a screen. A settlement reads it when it is computed.
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

	// InterestRateBps is the station's usual monthly interest rate, in basis
	// points: 500 is 5% a month.
	//
	// It is a default, not a rule. The legacy station typed the rate on every
	// settlement, and a settlement here starts with this figure and can be
	// changed — the legacy data has 1,063 settlements at 5%, 29 at 3% and two
	// at 0%, which is how a charge was waived. Changing it changes the next
	// settlement, never one already computed.
	//
	// Basis points rather than a percentage with a decimal point, because a
	// rate stored as "3.5" is a float waiting to be parsed into one.
	InterestRateBps = Def{
		Key:     "interest.rate_bps",
		Kind:    KindInt,
		Default: "500",
		Doc:     "The usual monthly interest rate in basis points — 500 is 5% a month. Each settlement starts with it and can change it.",
	}

	// RefNoStart is the number the advance-slip sequence continues from at
	// cutover, so staff see no discontinuity on the morning they switch.
	//
	// The default is the legacy system's next number as read from its data on
	// 2026-10-15: its counter and its highest Ref. No. both stood at 1425. It
	// moves if they keep encoding, so it is checked again on the day. It only
	// has an effect until the first slip is posted.
	RefNoStart = Def{
		Key:     "refno.start",
		Kind:    KindInt,
		Default: "1426",
		Doc:     "First Ref. No. this system issues. Continues the legacy sequence — check the old system's last number at cutover.",
	}
)

// All is every known setting, in the order a settings screen should show them.
// Seeding and the settings API both read this, so there is one list.
var All = []Def{
	Timezone,
	LANAccess,
	BackupSchedule,
	BackupEnabled,
	BackupRetention,
	RefNoStart,
	OrNoStart,
	PaymentApplies,
	InterestRateBps,
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
