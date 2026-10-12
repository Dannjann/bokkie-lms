package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
)

// cmdImportCustomers brings the legacy customer master in from a CSV file.
//
// This is the command run once, at the host, on the morning of the cutover, by
// whoever is setting the station up. The same import is available to a manager
// through the app afterwards; both go through the same parser and the same
// apply step, so there is one set of rules and one place they can be wrong.
//
// It runs against the live database while the service is running. SQLite's WAL
// handles a second writer, and "stop the service first" is an instruction that
// stops being followed within a week.
func cmdImportCustomers(args []string) error {
	fs := flag.NewFlagSet("import-customers", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	check := fs.Bool("check", false, "report what would be imported and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bokkie import-customers [-check] <file.csv>")
	}
	path := fs.Arg(0)

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	ctx := context.Background()

	cfg, log, closeLog, err := bootstrap(*dataDir, true)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(ctx, database, log); err != nil {
		return err
	}

	customers := customer.New(database, audit.New(database, log), log)

	existing, err := customers.ExistingCodes(ctx)
	if err != nil {
		return err
	}

	report, err := customer.Plan(file, existing)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	report.FileName = filepath.Base(path)

	printImportReport(report)

	// A dry run stops here, having touched nothing. It is not a description of
	// the import; it is the import minus the commit.
	if *check {
		if report.OK() {
			fmt.Printf("\nnothing was written. run without -check to import these %d customers.\n",
				report.Accepted)
		} else {
			fmt.Printf("\nnothing was written. fix the %d rows above and run -check again.\n",
				len(report.Rejections))
		}
		return nil
	}

	if !report.OK() {
		return fmt.Errorf("%d rows cannot be imported; nothing was written", len(report.Rejections))
	}

	applied, err := customers.Apply(ctx, report, customer.Console)
	if err != nil {
		return err
	}

	fmt.Printf("\nimported %d customers, %s to %s\n",
		applied.Accepted, applied.FirstCode, applied.LastCode)
	return nil
}

// printImportReport shows what the file says, rejections first — they are the
// reason somebody ran this, and burying them under a success count is how a
// refused import gets mistaken for a completed one.
func printImportReport(report customer.Report) {
	fmt.Printf("%s\n", report.FileName)
	fmt.Printf("  sha256    %s\n", report.SHA256)
	fmt.Printf("  readable  %s", plural(report.Accepted, "customer"))
	if report.Accepted > 0 {
		fmt.Printf(", %s to %s", report.FirstCode, report.LastCode)
	}
	fmt.Println()

	if report.OK() {
		return
	}

	fmt.Printf("  rejected  %s\n\n", plural(len(report.Rejections), "row"))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  LINE\tCODE\tWHY")
	for _, r := range report.Rejections {
		code := r.Code
		if code == "" {
			code = "—"
		}
		fmt.Fprintf(w, "  %d\t%s\t%s\n", r.Line, code, r.Reason)
	}
	w.Flush()

	// The whole file is refused, not the rows. Saying so here is the difference
	// between somebody fixing their file and somebody assuming the good rows
	// went in and typing the rest by hand on top of them.
	fmt.Println("\n  the import is all-or-nothing: none of these rows will be written.")
}

// plural spells a count with its noun. A report that says "1 customers" reads
// as a machine talking, and this one is read by somebody deciding whether to
// trust it with their whole customer master.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
