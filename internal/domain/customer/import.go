package customer

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
)

// ActionImported is the audit action for a completed import. Domain actions are
// named where they happen, which for this one is here rather than at a route:
// the same import runs from the command line at cutover and from a manager's
// browser afterwards, and both must write the same row.
const ActionImported = "customer.imported"

// MaxImportBytes caps what will be read. The real file is 364 rows — some tens
// of kilobytes — and this leaves room for a station that grows a hundredfold
// while still refusing to load something enormous into a machine that is also
// serving the counter.
const MaxImportBytes = 5 << 20

// The CSV columns. Matching on the header rather than on position means a
// column added to some future export does not shift everything by one.
const (
	colCode    = "code"
	colName    = "name"
	colAddress = "address"
	colCellNo  = "cell_no"
	colActive  = "active"
	colNotes   = "notes"
)

var knownColumns = []string{colCode, colName, colAddress, colCellNo, colActive, colNotes}

// Row is one customer the file is asking for, already validated.
type Row struct {
	Line   int
	Code   string
	Draft  Draft
	Active bool
}

// Rejection is one row the import will not take, and why. The line number is
// the one in the file, so it can be found in a spreadsheet without counting.
type Rejection struct {
	Line   int    `json:"line"`
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason"`
}

// Report is what an import would do, or did.
//
// It is the whole output of a dry run and the input to a real one, which is
// what makes the preview trustworthy: the preview is not a description of the
// import, it is the import minus the commit.
type Report struct {
	FileName   string      `json:"file_name,omitempty"`
	SHA256     string      `json:"sha256"`
	Accepted   int         `json:"accepted"`
	Rejections []Rejection `json:"rejections"`
	FirstCode  string      `json:"first_code,omitempty"`
	LastCode   string      `json:"last_code,omitempty"`
	Applied    bool        `json:"applied"`

	// rows is the plan itself. It is unexported and not in the JSON: a caller
	// reads the summary, and Apply is handed the whole Report back rather than
	// being trusted to reconstruct it.
	rows []Row
}

// OK reports whether the file is importable. Anything less than every row is a
// refusal — see Apply.
func (r Report) OK() bool { return len(r.Rejections) == 0 }

// Count is how many customers the report would write.
func (r Report) Count() int { return len(r.rows) }

// CodeSet is the codes already on the books, for detecting collisions without a
// query per row.
type CodeSet map[string]struct{}

// Has reports whether code is already taken.
func (s CodeSet) Has(code string) bool {
	_, ok := s[NormaliseCode(code)]
	return ok
}

// Add records a code as taken.
func (s CodeSet) Add(code string) { s[NormaliseCode(code)] = struct{}{} }

// ExistingCodes reads every code already on the books.
//
// Loading them once beats a query per row, and at this size the whole set is a
// few kilobytes. It also means Plan needs no database at all, which is what
// lets a dry run be a pure function of the file.
func (s *Service) ExistingCodes(ctx context.Context) (CodeSet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT code FROM customers`)
	if err != nil {
		return nil, fmt.Errorf("read existing codes: %w", err)
	}
	defer rows.Close()

	set := CodeSet{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("read existing codes: %w", err)
		}
		set.Add(code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read existing codes: %w", err)
	}
	return set, nil
}

// Plan parses and validates a file. It touches no database and changes nothing.
//
// The error return is for a file that cannot be understood at all — a missing
// header, an unknown column, unbalanced quotes. Anything wrong with an
// individual row is a Rejection instead, so that one dry run tells you
// everything you have to fix rather than one thing at a time.
func Plan(r io.Reader, existing CodeSet) (Report, error) {
	if existing == nil {
		existing = CodeSet{}
	}

	raw, err := io.ReadAll(io.LimitReader(r, MaxImportBytes+1))
	if err != nil {
		return Report{}, fmt.Errorf("read import file: %w", err)
	}
	if len(raw) > MaxImportBytes {
		return Report{}, fmt.Errorf("that file is larger than %d MB", MaxImportBytes>>20)
	}
	if len(raw) == 0 {
		return Report{}, errors.New("that file is empty")
	}

	sum := sha256.Sum256(raw)
	report := Report{SHA256: hex.EncodeToString(sum[:]), Rejections: []Rejection{}}

	// Excel on Windows writes a UTF-8 BOM, and that is where this file will
	// come from. Left in place it becomes part of the first header's name and
	// "code" is never found.
	body := strings.TrimPrefix(string(raw), "\uFEFF")

	reader := csv.NewReader(strings.NewReader(body))
	reader.TrimLeadingSpace = true
	// Arity is checked per row below, which gives a message naming the line
	// instead of aborting the whole read on the first ragged row.
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return Report{}, fmt.Errorf("read the header row: %w", err)
	}
	index, err := headerIndex(header)
	if err != nil {
		return Report{}, err
	}

	seen := CodeSet{}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Report{}, fmt.Errorf("read the file: %w", err)
		}

		line, _ := reader.FieldPos(0)

		if isBlank(record) {
			// A spreadsheet export routinely ends with an empty line, or a row
			// of nothing but commas. Rejecting those would fail an import for
			// a reason nobody can see by looking at the file.
			continue
		}

		if len(record) != len(header) {
			report.reject(line, "", fmt.Sprintf(
				"this row has %d columns and the header has %d", len(record), len(header)))
			continue
		}

		row, rejection := planRow(line, record, index, existing, seen)
		if rejection != nil {
			report.Rejections = append(report.Rejections, *rejection)
			continue
		}
		seen.Add(row.Code)
		report.rows = append(report.rows, row)
	}

	if len(report.rows) == 0 && report.OK() {
		return Report{}, errors.New("that file has a header but no customers in it")
	}

	report.Accepted = len(report.rows)
	report.summariseCodes()
	return report, nil
}

// headerIndex maps known column names to their position, and refuses anything
// it does not recognise.
//
// An unknown column is an error rather than something to ignore. A header
// reading "cellno" where "cell_no" was meant would otherwise import 364
// customers with no phone numbers at all and look like it had worked.
func headerIndex(header []string) (map[string]int, error) {
	index := make(map[string]int, len(header))
	var unknown []string

	for i, name := range header {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil, fmt.Errorf("column %d of the header row has no name", i+1)
		}
		if !known(name) {
			unknown = append(unknown, name)
			continue
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("the header row has two %q columns", name)
		}
		index[name] = i
	}

	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown column %s — this file should have %s",
			quoteList(unknown), strings.Join(knownColumns, ", "))
	}
	for _, required := range []string{colCode, colName} {
		if _, ok := index[required]; !ok {
			return nil, fmt.Errorf("the header row has no %q column", required)
		}
	}
	return index, nil
}

// planRow validates one line into a Row, or explains why it cannot.
func planRow(line int, record []string, index map[string]int, existing, seen CodeSet) (Row, *Rejection) {
	field := func(name string) string {
		i, ok := index[name]
		if !ok {
			return ""
		}
		return strings.TrimSpace(record[i])
	}

	code := NormaliseCode(field(colCode))
	if err := ValidateCode(code); err != nil {
		return Row{}, &Rejection{Line: line, Code: code, Reason: err.Error()}
	}
	switch {
	case seen.Has(code):
		return Row{}, &Rejection{Line: line, Code: code,
			Reason: "this code appears more than once in the file"}
	case existing.Has(code):
		return Row{}, &Rejection{Line: line, Code: code,
			Reason: "a customer with this code is already on the books"}
	}

	active, err := parseActive(field(colActive))
	if err != nil {
		return Row{}, &Rejection{Line: line, Code: code, Reason: err.Error()}
	}

	draft, err := Draft{
		Name:    field(colName),
		Address: field(colAddress),
		CellNo:  field(colCellNo),
		Notes:   field(colNotes),
	}.clean()
	if err != nil {
		return Row{}, &Rejection{Line: line, Code: code, Reason: err.Error()}
	}

	return Row{Line: line, Code: code, Draft: draft, Active: active}, nil
}

// parseActive reads the active flag generously, because a column like this
// arrives spelled differently from every export, and refuses what it cannot
// read rather than guessing.
func parseActive(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "1", "true", "t", "yes", "y", "active":
		return true, nil
	case "0", "false", "f", "no", "n", "inactive":
		return false, nil
	default:
		return false, fmt.Errorf("%q is not a yes or a no in the active column", s)
	}
}

// Apply writes a plan, all of it or none of it.
//
// A file with one bad row writes nothing. A half-imported master is not a
// smaller problem than a failed import — it is the same problem plus the belief
// that the job is done, and the person holding that belief is the one who then
// types the remaining 60 customers by hand on top of the 300 already there.
func (s *Service) Apply(ctx context.Context, rep Report, by Actor) (Report, error) {
	if !rep.OK() {
		return rep, fmt.Errorf("this file has %d rows that cannot be imported; nothing was written",
			len(rep.Rejections))
	}
	if len(rep.rows) == 0 {
		return rep, errors.New("there is nothing in this file to import")
	}
	if by.Username == "" {
		return rep, errors.New("an import must record who ran it")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rep, fmt.Errorf("import customers: %w", err)
	}
	defer tx.Rollback()

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	for _, row := range rep.rows {
		if err := insertWithCode(ctx, tx, row, stamp); err != nil {
			return rep, fmt.Errorf("line %d (%s): %w", row.Line, row.Code, err)
		}
	}

	// One entry for the whole import, not 364. A row per customer would bury
	// every other thing that happened that day, and the question anyone asks
	// later is "where did this master come from", not "when was BA-0203
	// written". The file's hash is what makes that question answerable: it
	// identifies which file produced these records, in the same spirit as a
	// signed release and a verified backup.
	if err := s.auditor.RecordTx(ctx, tx, audit.Entry{
		UserID:   by.UserID,
		Username: by.Username,
		Action:   ActionImported,
		Entity:   "customer",
		EntityID: rep.FirstCode + "…" + rep.LastCode,
		After:    rep,
	}); err != nil {
		return rep, fmt.Errorf("import customers: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return rep, fmt.Errorf("import customers: %w", err)
	}

	rep.Applied = true
	return rep, nil
}

func insertWithCode(ctx context.Context, db queryExecer, row Row, stamp string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO customers (code, name, address, cell_no, active, notes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Code, row.Draft.Name, row.Draft.Address, row.Draft.CellNo,
		boolToInt(row.Active), row.Draft.Notes, stamp, stamp)
	if err != nil {
		if isUniqueViolation(err) {
			// Reachable only if somebody wrote this code between the dry run
			// and the commit. The whole transaction rolls back.
			return ErrCodeTaken
		}
		return err
	}
	return nil
}

func (r *Report) reject(line int, code, reason string) {
	r.Rejections = append(r.Rejections, Rejection{Line: line, Code: code, Reason: reason})
}

// summariseCodes records the range written, for the audit entry. The rows are
// in file order, which need not be code order, so this is a scan rather than
// first and last.
func (r *Report) summariseCodes() {
	for _, row := range r.rows {
		if r.FirstCode == "" || row.Code < r.FirstCode {
			r.FirstCode = row.Code
		}
		if row.Code > r.LastCode {
			r.LastCode = row.Code
		}
	}
}

func known(name string) bool {
	for _, c := range knownColumns {
		if c == name {
			return true
		}
	}
	return false
}

func isBlank(record []string) bool {
	for _, f := range record {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}
