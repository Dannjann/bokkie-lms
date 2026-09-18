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

	// InterestMethod is how interest is computed, if at all.
	//
	// This is Q1 of the domain interview — the single most important question
	// about how the business works — and it is unanswered. It is a setting
	// rather than a constant for the reason PaymentApplies is one, except that
	// here the stakes are higher: an interest rule implemented wrong produces
	// numbers that look plausible and are wrong across every farmer at once.
	//
	// The default is "none", which charges nothing and refuses to let a run be
	// created at all. An install nobody has configured therefore computes no
	// interest, rather than quietly computing some — and if the answer to Q1
	// turns out to be "that button was never used", the answer costs a settings
	// screen and no code.
	InterestMethod = Def{
		Key:     "interest.method",
		Kind:    KindChoice,
		Default: "none",
		Options: []string{"none", "monthly-on-balance", "flat-on-advance"},
		Doc:     "How interest is computed: not at all, on the balance at the end of the period, or once on what was advanced during it.",
	}

	// InterestRateBps is the rate in basis points: 300 is 3%, 25 is a quarter
	// of one percent.
	//
	// Basis points rather than a percentage with a decimal point, because a
	// rate stored as "3.5" is a float waiting to be parsed into one, and
	// internal/domain/money has a test that walks the syntax tree and fails on
	// any float at all. An integer against a denominator of 10,000 is exact.
	InterestRateBps = Def{
		Key:     "interest.rate_bps",
		Kind:    KindInt,
		Default: "0",
		Doc:     "The interest rate in basis points — 300 is 3%. Zero charges nothing.",
	}

	// InterestBasis is what "what they owe" means when interest is charged on
	// a balance: the principal alone, or the principal plus interest already
	// charged and not yet paid.
	//
	// That is the difference between simple and compound interest, it is a
	// sub-question of Q1, and it changes what money does — which is why it is a
	// choice refused at the edge rather than a bool that would leave a screen
	// having to invent a word for each side.
	InterestBasis = Def{
		Key:     "interest.basis",
		Kind:    KindChoice,
		Default: "principal",
		Options: []string{"principal", "principal-and-interest"},
		Doc:     "Whether interest is charged on unpaid interest as well as on principal.",
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
	LANAccess,
	BackupSchedule,
	BackupEnabled,
	BackupRetention,
	RefNoStart,
	OrNoStart,
	PaymentApplies,
	InterestMethod,
	InterestRateBps,
	InterestBasis,
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
