// Package update replaces the installed bokkie binary with a newer one.
//
// The sequence, and the order of it, is the whole package:
//
//	verify the signature → verify it is newer → back the database up →
//	stop → swap → start → confirm /healthz answers with the new version
//
// and, if any step after the swap fails, put the old binary back and start it
// again. Nothing here is clever. It is a list of things that must happen in one
// order, on a machine nobody is watching, where the failure mode is a buying
// station that cannot record an advance in the morning.
//
// Where the release came from is not this package's business. Today `bokkie
// update` is handed a folder from a USB stick. The day that becomes an HTTPS
// download, only the caller changes.
package update

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/release"
	"github.com/Dannjann/bokkie-lms/internal/service"
)

// Options is everything Apply needs to know. It is deliberately explicit rather
// than reaching for configuration itself, so a test can drive the whole
// sequence without a config file or a service manager.
type Options struct {
	// Dir is the release folder: the binaries, latest.json, latest.json.sig.
	Dir string

	// Service identifies the installation being updated.
	Service service.Options

	// Backup is where the pre-update backup goes and how many to keep. It is
	// the same folder and count the nightly job uses.
	Backup backup.Options

	// Database is the live database, used for the backup and the audit row.
	Database *sql.DB

	// Audit records the outcome. Optional only so that a test need not build
	// one; in the command it is always present.
	Audit audit.Recorder

	// Force installs a version that is not newer than the running one. It
	// exists for the case where a release has to be pulled back, and it is not
	// the same as it being safe: an older binary will refuse to start against a
	// schema a newer one has already migrated, which is exactly why the backup
	// above is taken first.
	Force bool
}

// Result is what happened, for the log line and the message printed to whoever
// ran the command.
type Result struct {
	From       string
	To         string
	BackupPath string
	Notes      string
}

// Apply performs the update, or leaves the machine exactly as it found it.
func Apply(ctx context.Context, log *slog.Logger, opts Options) (Result, error) {
	result := Result{From: opts.installedVersion(log)}

	// 1. Nothing in the release folder is believed before its signature is.
	manifest, err := release.Load(opts.Dir)
	if err != nil {
		return result, opts.failed(ctx, log, result, err)
	}
	result.To, result.Notes = manifest.Version, manifest.Notes

	artifact, err := manifest.ForHost()
	if err != nil {
		return result, opts.failed(ctx, log, result, err)
	}

	// 2. Newer, unless someone has explicitly said otherwise.
	if err := opts.checkDirection(manifest.Version, result.From); err != nil {
		return result, opts.failed(ctx, log, result, err)
	}

	// 3. The binary is the one the signed manifest describes.
	source := filepath.Join(opts.Dir, artifact.File)
	if err := artifact.Verify(source); err != nil {
		return result, opts.failed(ctx, log, result, err)
	}
	log.Info("release verified", "version", manifest.Version, "platform", artifact.Platform, "file", artifact.File)

	// 4. A backup, before anything is touched. This is what makes the rest
	//    reversible, and it is why an update cannot be made to skip it.
	backupResult, err := backup.Run(ctx, opts.Database, opts.Backup)
	if err != nil {
		return result, opts.failed(ctx, log, result,
			fmt.Errorf("take a backup before updating (nothing has been changed): %w", err))
	}
	result.BackupPath = backupResult.Path
	log.Info("pre-update backup written and verified", "backup", backupResult)

	// 5. Everything from here can fail with the binary already swapped, so
	//    everything from here has a way back.
	if err := opts.swap(ctx, log, &result, source); err != nil {
		return result, err
	}

	if opts.Audit != nil {
		_ = opts.Audit.Record(ctx, audit.Entry{
			At:       time.Now().UTC(),
			Username: audit.ConsoleUser,
			Action:   audit.ActionUpdateApplied,
			Entity:   "system",
			Before:   map[string]string{"version": result.From},
			After:    map[string]string{"version": result.To, "backup": result.BackupPath},
		})
	}
	log.Info("update applied", "from", result.From, "to", result.To, "backup", result.BackupPath)
	return result, nil
}

// swap is the part with a rollback. It is separated from Apply so that the
// "undo everything" path is one function with one exit rather than a sequence
// of ifs that each have to remember what came before them.
func (opts Options) swap(ctx context.Context, log *slog.Logger, result *Result, source string) error {
	installed, err := service.IsInstalled(opts.Service)
	if err != nil {
		return opts.failed(ctx, log, *result, err)
	}

	if installed {
		if err := service.Stop(log, opts.Service); err != nil {
			return opts.failed(ctx, log, *result, err)
		}
	} else {
		// The ordinary case on a Windows station since step 2.7: the station
		// runs while a person has its window open, so there is no service to
		// stop and nothing to restart. Said at info level, not warn — it is
		// how this host is meant to work, not a fault.
		log.Info("no bokkie service is registered; the new version takes effect the next time the window is opened")
	}

	restore, err := service.Swap(log, source)
	if err != nil {
		// The swap itself failed, so there is nothing to undo — but the service
		// is stopped and must not be left that way.
		if installed {
			if startErr := service.Start(log, opts.Service); startErr != nil {
				log.Error("could not restart the service after a failed swap", "error", startErr)
			}
		}
		return opts.failed(ctx, log, *result, err)
	}

	if !installed {
		// With no service to bring up, nothing else in this function would
		// ever look at the binary that was just put in place — and a swap that
		// produced an unrunnable file would not be discovered until somebody
		// opened the window and got nothing. Asking it its version is the
		// cheapest proof that it runs at all.
		if err := confirmReplacement(log, result.To); err != nil {
			log.Error("the new binary does not run; putting the previous one back", "error", err)
			return opts.rollBackFile(ctx, log, *result, restore, err)
		}
		service.Discard(log)
		return nil
	}

	if err := opts.startAndConfirm(log, result.To); err != nil {
		log.Error("the new binary did not come up; putting the previous one back", "error", err)
		return opts.rollBack(ctx, log, *result, restore, err)
	}

	service.Discard(log)
	return nil
}

// confirmReplacement runs the binary that was just put in place and asks it
// which version it is.
//
// It is the no-service equivalent of startAndConfirm, and it checks the version
// for the same reason: a file that runs but reports the old version means the
// swap did not take, and calling that a successful update would leave somebody
// believing a fix is on the machine when it is not.
func confirmReplacement(log *slog.Logger, want string) error {
	path, err := service.InstalledPath()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "version", "-short").Output()
	if err != nil {
		return fmt.Errorf("the replaced binary at %s would not run: %w", path, err)
	}

	got := strings.TrimSpace(string(out))
	if got != want {
		return fmt.Errorf("the replaced binary reports version %s, not %s: the swap did not take effect", got, want)
	}
	log.Info("the new binary runs and reports the expected version", "version", got, "path", path)
	return nil
}

// confirmTimeout bounds asking a binary its version. It prints one line and
// exits; anything slower is a binary that is not going to answer.
const confirmTimeout = 15 * time.Second

// rollBackFile puts the previous binary back when there is no service involved.
//
// Separate from rollBack because that one restarts a service afterwards, and
// doing so here would start something this host is deliberately not running.
func (opts Options) rollBackFile(ctx context.Context, log *slog.Logger, result Result, restore func() error, cause error) error {
	if err := restore(); err != nil {
		return fmt.Errorf("the update failed (%w) and the previous binary could not be put back: %w.\n"+
			"Copy a known-good bokkie to the installed path by hand", cause, err)
	}
	log.Info("the previous binary was put back")
	return opts.failed(ctx, log, result, cause)
}

// startAndConfirm is the difference between "the service manager spawned
// something" and "bokkie is serving". It checks the version too: a service that
// answers with the *old* version means the restart did not take, and calling
// that a successful update would leave everyone believing a fix is deployed
// when it is not.
func (opts Options) startAndConfirm(log *slog.Logger, want string) error {
	if err := service.Start(log, opts.Service); err != nil {
		return err
	}
	serving, err := service.WaitUntilHealthy(log, opts.Service.Port)
	if err != nil {
		return err
	}
	if serving != want {
		return fmt.Errorf("the service came up still reporting version %s, not %s: the restart did not take effect", serving, want)
	}
	return nil
}

func (opts Options) rollBack(ctx context.Context, log *slog.Logger, result Result, restore func() error, cause error) error {
	if err := restore(); err != nil {
		// The worst outcome in this package: a host with no working binary.
		// Say exactly what to do about it rather than only what went wrong.
		return fmt.Errorf("the update failed (%w) and the previous binary could not be put back: %w.\n"+
			"Copy a known-good bokkie to the installed path by hand and start the service", cause, err)
	}
	if err := service.Start(log, opts.Service); err != nil {
		return fmt.Errorf("the update failed (%w), the previous binary was put back, but the service would not start: %w", cause, err)
	}
	if _, err := service.WaitUntilHealthy(log, opts.Service.Port); err != nil {
		return fmt.Errorf("the update failed (%w), the previous binary was put back, but it is not answering: %w", cause, err)
	}

	if opts.Audit != nil {
		_ = opts.Audit.Record(ctx, audit.Entry{
			At:       time.Now().UTC(),
			Username: audit.ConsoleUser,
			Action:   audit.ActionUpdateRolledBack,
			Entity:   "system",
			Before:   map[string]string{"version": result.From},
			After:    map[string]string{"attempted": result.To, "reason": cause.Error(), "backup": result.BackupPath},
		})
	}
	log.Warn("update rolled back; the previous version is running again", "version", result.From, "attempted", result.To)
	return fmt.Errorf("%s did not come up, so %s was put back and is running again: %w", result.To, result.From, cause)
}

// installedVersion is the version actually serving on this host, which is not
// necessarily the version of the process asking.
//
// Running `bokkie update` from the new binary on the USB stick is the obvious
// thing to do and has to work: comparing that binary's own version against the
// release it came from would say "you are already running this" and refuse the
// update that was the whole point of walking over.
func (opts Options) installedVersion(log *slog.Logger) string {
	serving, err := service.ServingVersion(opts.Service.Port)
	if err != nil {
		// Nothing is answering — a host where bokkie is stopped, or not
		// installed at all. The running process's own version is then the best
		// available answer, and checkDirection is careful about what it will
		// conclude from it.
		log.Debug("no running bokkie to ask for its version", "error", err)
		return buildinfo.Version
	}
	return serving
}

// checkDirection refuses anything that is not a step forward.
//
// A build that is not an exact release tag has no place on the station's host
// at all, and the message says why rather than just refusing.
func (opts Options) checkDirection(candidate, installed string) error {
	next, err := release.ParseVersion(candidate)
	if err != nil {
		return err
	}

	current, err := release.ParseVersion(installed)
	if err != nil {
		// What is installed is a development build. There is no sensible
		// comparison to make, so the only question is whether someone meant it.
		if !opts.Force {
			return fmt.Errorf("the bokkie installed here is %s, a development build, so there is no version to compare %s against; "+
				"re-run with -force if you mean to replace it", installed, candidate)
		}
		return nil
	}

	if !next.NewerThan(current) && !opts.Force {
		return fmt.Errorf("%s is not newer than the %s already running; re-run with -force if you mean to go back",
			candidate, current)
	}
	return nil
}

func (opts Options) failed(ctx context.Context, log *slog.Logger, result Result, cause error) error {
	if opts.Audit != nil {
		_ = opts.Audit.Record(ctx, audit.Entry{
			At:       time.Now().UTC(),
			Username: audit.ConsoleUser,
			Action:   audit.ActionUpdateFailed,
			Entity:   "system",
			Before:   map[string]string{"version": result.From},
			After:    map[string]string{"attempted": result.To, "reason": cause.Error()},
		})
	}
	log.Error("update refused", "from", result.From, "attempted", result.To, "error", cause)
	return cause
}
