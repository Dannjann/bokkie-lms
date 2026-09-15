package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/release"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/settings"
	"github.com/Dannjann/bokkie-lms/internal/update"
)

// cmdUpdate replaces the installed binary with the one in a release folder.
//
// It takes a folder rather than a file because a release is not just a binary:
// it is a binary plus the signed manifest that says which binary it is. Handed
// only an executable there would be nothing to check it against, and "check it
// against the file sitting next to it" is not a check at all.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	check := fs.Bool("check", false, "say what the release folder holds, and change nothing")
	force := fs.Bool("force", false, "install a version that is not newer than the one running")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := fs.Arg(0)
	if dir == "" {
		return fmt.Errorf("which release folder? usage: bokkie update [flags] <folder>\n" +
			"That is the folder on the USB stick holding the binaries and latest.json")
	}

	// -check runs before anything is opened or locked. It is the command to
	// give someone over the phone: it proves the stick is genuine and says
	// what is on it, without touching the machine.
	if *check {
		return describeRelease(dir)
	}

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

	store, err := settings.Open(ctx, database)
	if err != nil {
		return err
	}

	result, err := update.Apply(ctx, log, update.Options{
		Dir: dir,
		Service: service.Options{
			DataDir: cfg.DataDir,
			Port:    cfg.Port,
			LogDir:  cfg.LogDir(),
		},
		Backup: backup.Options{
			Folder:    cfg.Backup.Folder,
			Retention: store.Int(settings.BackupRetention),
		},
		Database: database,
		Audit:    audit.New(database, log),
		Force:    *force,
	})
	if err != nil {
		return err
	}

	fmt.Printf("updated %s → %s\n", result.From, result.To)
	if result.Notes != "" {
		fmt.Printf("  %s\n", result.Notes)
	}
	fmt.Printf("  the database was backed up first: %s\n", result.BackupPath)
	fmt.Printf("  open http://bokkie.local:%d and check the version on the home page\n", cfg.Port)
	return nil
}

// describeRelease verifies a release folder and prints what it contains.
func describeRelease(dir string) error {
	manifest, err := release.Load(dir)
	if err != nil {
		return err
	}
	artifact, err := manifest.ForHost()
	if err != nil {
		return err
	}

	fmt.Printf("%s is a genuine bokkie release\n", dir)
	fmt.Printf("  version   %s (this machine is running %s)\n", manifest.Version, buildinfo.Version)
	fmt.Printf("  released  %s\n", manifest.Released)
	if manifest.Notes != "" {
		fmt.Printf("  notes     %s\n", manifest.Notes)
	}
	fmt.Printf("  for %s: %s\n", artifact.Platform, artifact.File)

	if err := artifact.Verify(dir + "/" + artifact.File); err != nil {
		return err
	}
	fmt.Println("  the binary matches the signed checksum")
	return nil
}
