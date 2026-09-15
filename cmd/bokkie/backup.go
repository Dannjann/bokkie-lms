package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// cmdBackup writes a verified backup right now.
//
// It runs against the live database while the service is running. That is safe
// and deliberate: VACUUM INTO takes a consistent snapshot through the same WAL
// machinery a reader uses, so nothing has to be stopped first — which matters,
// because "stop the service to take a backup" is an instruction that stops
// being followed within a week.
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	folder := fs.String("to", "", "write the backup here instead of the configured folder")
	list := fs.Bool("list", false, "list existing backups instead of writing one")
	verify := fs.String("verify", "", "check an existing backup file and print its row counts")
	setFolder := fs.String("set-folder", "", "write future backups here — a USB stick or a second disk")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()

	// -verify needs no configuration and no database: it is the command
	// someone runs when holding a USB stick and wondering whether the file on
	// it is any good.
	if *verify != "" {
		counts, err := backup.Verify(ctx, *verify)
		if err != nil {
			return err
		}
		fmt.Printf("%s verified\n", *verify)
		printCounts(counts)
		return nil
	}

	// -set-folder is likewise its own command. It changes where backups go and
	// writes nothing, so it does not need the database open.
	if *setFolder != "" {
		return setBackupFolder(*dataDir, *setFolder)
	}

	cfg, log, closeLog, err := bootstrap(*dataDir, true)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	destination := cfg.Backup.Folder
	if *folder != "" {
		destination = *folder
	}

	if *list {
		files, err := backup.List(destination)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			fmt.Printf("no backups in %s\n", destination)
			return nil
		}
		fmt.Printf("backups in %s, oldest first:\n", destination)
		for _, file := range files {
			fmt.Println(" ", file)
		}
		return nil
	}

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer database.Close()

	// The retention count lives in the settings table, so it is read from the
	// database rather than from config.yaml — and reading it here means the CLI
	// honours the same number the nightly job does.
	store, err := settings.Open(ctx, database)
	if err != nil {
		return err
	}

	result, err := backup.Run(ctx, database, backup.Options{
		Folder:    destination,
		Retention: store.Int(settings.BackupRetention),
	})
	if err != nil {
		return err
	}

	log.Info("backup written and verified", "backup", result)
	fmt.Printf("backup written and verified: %s (%s)\n", result.Path, humanBytes(result.Bytes))
	printCounts(result.RowCounts)
	for _, pruned := range result.Pruned {
		fmt.Printf("  pruned old backup %s\n", pruned)
	}
	return nil
}

// setBackupFolder points backups at a new destination, usually a USB stick.
//
// The folder is checked before it is saved, and the restart is spelled out
// rather than implied: the folder is read when the service starts, so a change
// made at the counter does nothing until the service is restarted, and an owner
// who does not know that will believe their backups are on the stick when they
// are not.
func setBackupFolder(dataDir, folder string) error {
	cfg, err := config.SetBackupFolder(dataDir, folder)
	if err != nil {
		return err
	}

	fmt.Printf("backups will be written to %s\n", cfg.Backup.Folder)
	fmt.Printf("  saved in %s\n", cfg.ConfigPath())
	fmt.Printf("  restart the service for this to take effect, then run `bokkie backup` to confirm\n")
	return nil
}

func printCounts(counts map[string]int) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  table\trows")
	for _, table := range backup.VerifiedTables() {
		fmt.Fprintf(w, "  %s\t%d\n", table, counts[table])
	}
	w.Flush()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
