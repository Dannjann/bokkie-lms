package customer

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const goodHeader = "code,name,address,cell_no,active,notes\n"

func planString(t *testing.T, body string, existing CodeSet) (Report, error) {
	t.Helper()
	return Plan(strings.NewReader(body), existing)
}

func mustPlan(t *testing.T, body string, existing CodeSet) Report {
	t.Helper()
	rep, err := planString(t, body, existing)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return rep
}

func TestPlanReadsAWellFormedFile(t *testing.T) {
	rep := mustPlan(t, goodHeader+
		"BA-0001,Juan dela Cruz,Pintor Gamu,0917-123-4567,true,\n"+
		"BA-0002,Maria Santos,Gamu,0918 765 4321,no,seasonal\n", nil)

	if !rep.OK() {
		t.Fatalf("unexpected rejections: %+v", rep.Rejections)
	}
	if rep.Accepted != 2 {
		t.Errorf("accepted %d, want 2", rep.Accepted)
	}
	if rep.FirstCode != "BA-0001" || rep.LastCode != "BA-0002" {
		t.Errorf("code range %s…%s", rep.FirstCode, rep.LastCode)
	}
	if rep.rows[1].Active {
		t.Error("the second row said no in the active column")
	}
	if got := rep.rows[0].Draft.CellNo; got != "09171234567" {
		t.Errorf("cell %q was not normalised", got)
	}
	if rep.SHA256 == "" {
		t.Error("no hash recorded")
	}
}

// Excel on Windows writes a UTF-8 BOM, and that is where this file will come
// from. Left in place it becomes part of the first header's name.
func TestPlanStripsTheExcelByteOrderMark(t *testing.T) {
	rep := mustPlan(t, "\uFEFF"+goodHeader+"BA-0001,Juan dela Cruz,,,,\n", nil)
	if !rep.OK() {
		t.Fatalf("BOM was not stripped: %+v", rep.Rejections)
	}
}

func TestPlanHandlesCRLF(t *testing.T) {
	body := strings.ReplaceAll(goodHeader+"BA-0001,Juan dela Cruz,,,,\n", "\n", "\r\n")
	rep := mustPlan(t, body, nil)
	if !rep.OK() {
		t.Fatalf("CRLF was not handled: %+v", rep.Rejections)
	}
	if got := rep.rows[0].Draft.Name; got != "Juan dela Cruz" {
		t.Errorf("name is %q — a carriage return survived", got)
	}
}

// A spreadsheet export routinely ends with a blank line or a row of commas.
func TestPlanSkipsBlankRows(t *testing.T) {
	rep := mustPlan(t, goodHeader+"BA-0001,Juan dela Cruz,,,,\n\n,,,,,\n", nil)
	if !rep.OK() {
		t.Fatalf("blank rows were rejected: %+v", rep.Rejections)
	}
	if rep.Accepted != 1 {
		t.Errorf("accepted %d, want 1", rep.Accepted)
	}
}

// The whole file is refused, not the row: a header reading "cellno" would
// otherwise import every customer with no phone number and look like it worked.
func TestPlanRefusesAnUnknownColumn(t *testing.T) {
	_, err := planString(t, "code,name,cellno\nBA-0001,Juan,0917\n", nil)
	if err == nil {
		t.Fatal("an unknown column was accepted")
	}
	if !strings.Contains(err.Error(), "cellno") {
		t.Errorf("the error does not name the column: %v", err)
	}
}

func TestPlanRefusesAFileWithoutTheRequiredColumns(t *testing.T) {
	for _, header := range []string{"name,address\n", "code,address\n"} {
		if _, err := planString(t, header+"x,y\n", nil); err == nil {
			t.Errorf("header %q was accepted", strings.TrimSpace(header))
		}
	}
}

func TestPlanRefusesADuplicateHeader(t *testing.T) {
	if _, err := planString(t, "code,name,name\nBA-0001,Juan,Juan\n", nil); err == nil {
		t.Fatal("a duplicated column was accepted")
	}
}

func TestPlanRejectsBadRowsIndividually(t *testing.T) {
	existing := CodeSet{}
	existing.Add("BA-0009")

	rep := mustPlan(t, goodHeader+
		"BA-0001,Juan dela Cruz,,,,\n"+ // fine
		"BA-1,Short Code,,,,\n"+ // malformed code
		"BA-0002,,,,,\n"+ // no name
		"BA-0001,Repeat In File,,,,\n"+ // duplicate within the file
		"BA-0009,Already On The Books,,,,\n"+ // duplicate against the database
		"BA-0003,Bad Flag,,,maybe,\n", // unreadable active column
		existing)

	if rep.Accepted != 1 {
		t.Errorf("accepted %d, want 1", rep.Accepted)
	}
	if len(rep.Rejections) != 5 {
		t.Fatalf("got %d rejections, want 5: %+v", len(rep.Rejections), rep.Rejections)
	}

	// Every rejection must name the line, so it can be found in a spreadsheet
	// without counting.
	wantLines := []int{3, 4, 5, 6, 7}
	for i, r := range rep.Rejections {
		if r.Line != wantLines[i] {
			t.Errorf("rejection %d is on line %d, want %d (%s)", i, r.Line, wantLines[i], r.Reason)
		}
		if r.Reason == "" {
			t.Errorf("rejection on line %d has no reason", r.Line)
		}
	}
}

func TestPlanRejectsARaggedRow(t *testing.T) {
	rep := mustPlan(t, goodHeader+
		"BA-0001,Juan dela Cruz,,,,\n"+
		"BA-0002,Too Few Columns\n", nil)

	if len(rep.Rejections) != 1 {
		t.Fatalf("got %d rejections, want 1", len(rep.Rejections))
	}
	if !strings.Contains(rep.Rejections[0].Reason, "columns") {
		t.Errorf("reason does not mention the column count: %s", rep.Rejections[0].Reason)
	}
}

// The headline guarantee: one bad row and nothing at all is written.
func TestABadRowAtTheEndImportsNothing(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	var b strings.Builder
	b.WriteString(goodHeader)
	for i := 1; i <= 299; i++ {
		fmt.Fprintf(&b, "BA-%04d,Farmer %d,Gamu,0917%07d,true,\n", i, i, i)
	}
	b.WriteString("BA-NOPE,Bad Row,,,,\n")

	rep := mustPlan(t, b.String(), nil)
	if rep.Accepted != 299 {
		t.Fatalf("accepted %d, want 299", rep.Accepted)
	}
	if rep.OK() {
		t.Fatal("the bad row was not rejected")
	}

	if _, err := svc.Apply(ctx, rep, Console); err == nil {
		t.Fatal("Apply accepted a report with rejections")
	}

	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d customers were written by a refused import", count)
	}
}

// A dry run is Plan alone, which is what makes the preview trustworthy: it is
// not a description of the import, it is the import minus the commit.
func TestADryRunWritesNothing(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	existing, err := svc.ExistingCodes(ctx)
	if err != nil {
		t.Fatalf("existing codes: %v", err)
	}

	rep := mustPlan(t, goodHeader+"BA-0001,Juan dela Cruz,Gamu,0917-123-4567,true,\n", existing)
	if !rep.OK() || rep.Accepted != 1 {
		t.Fatalf("plan: %+v", rep)
	}
	if rep.Applied {
		t.Error("a plan reports itself as applied")
	}

	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a dry run wrote %d rows", count)
	}
}

func TestApplyWritesEveryRowAndOneAuditEntry(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	rep := mustPlan(t, goodHeader+
		"BA-0001,Juan dela Cruz,Pintor Gamu,0917-123-4567,true,\n"+
		"BA-0002,Maria Santos,Gamu,,false,seasonal\n", nil)

	applied, err := svc.Apply(ctx, rep, Console)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !applied.Applied {
		t.Error("the returned report does not say it was applied")
	}

	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("wrote %d customers, want 2", count)
	}

	var inactive int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customers WHERE active = 0`).Scan(&inactive); err != nil {
		t.Fatalf("count inactive: %v", err)
	}
	if inactive != 1 {
		t.Errorf("%d inactive customers, want 1 — the active column was not honoured", inactive)
	}

	// One entry for the whole import, not one per customer.
	var entries int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = ?`, ActionImported).Scan(&entries); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if entries != 1 {
		t.Fatalf("%d audit entries for one import, want 1", entries)
	}

	// The hash is what makes "which file produced this master?" answerable.
	var after string
	if err := database.QueryRowContext(ctx,
		`SELECT after_json FROM audit_log WHERE action = ?`, ActionImported).Scan(&after); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(after, rep.SHA256) {
		t.Error("the audit entry does not record the file's hash")
	}
}

func TestApplyRefusesAnImportWithNobodyBehindIt(t *testing.T) {
	svc, _ := openTestService(t)

	rep := mustPlan(t, goodHeader+"BA-0001,Juan dela Cruz,,,,\n", nil)
	if _, err := svc.Apply(context.Background(), rep, Actor{}); err == nil {
		t.Fatal("an import with no actor was accepted")
	}
}

func TestPlanRefusesAnEmptyOrHeaderOnlyFile(t *testing.T) {
	if _, err := planString(t, "", nil); err == nil {
		t.Error("an empty file was accepted")
	}
	if _, err := planString(t, goodHeader, nil); err == nil {
		t.Error("a header-only file was accepted")
	}
}
