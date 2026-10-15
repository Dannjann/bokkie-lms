package backup

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// Scheduler runs the nightly backup.
//
// It re-reads the schedule from settings before every sleep rather than
// computing a ticker once at startup. That is what makes "change the backup
// time" take effect the same evening instead of after the next restart — and a
// restart on this machine means an upgrade, which may be months away.
type Scheduler struct {
	db       *sql.DB
	settings *settings.Store
	folder   string
	log      *slog.Logger

	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	started atomic.Bool

	// running serialises backups. Nothing used to be able to ask for one while
	// the nightly job was mid-VACUUM; now closing the window does, and two at
	// once would leave a file that fails its own verification — which is worse
	// than one fewer backup, because somebody will trust it.
	running sync.Mutex
}

// NewScheduler builds the nightly job. folder comes from config.yaml, not from
// settings: it is a path on this particular machine — often a USB stick — and
// a path stored in the database would be wrong the moment the database is
// restored onto a different host.
func NewScheduler(database *sql.DB, store *settings.Store, folder string, log *slog.Logger) *Scheduler {
	return &Scheduler{
		db:       database,
		settings: store,
		folder:   folder,
		log:      log,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins the schedule in the background. Calling it twice is harmless.
func (s *Scheduler) Start() {
	if s.started.Swap(true) {
		return
	}
	go s.loop()
}

// Stop ends the schedule and waits for an in-flight backup to finish. A backup
// interrupted halfway leaves a partial file that would fail verification, so it
// is worth waiting out.
//
// It returns immediately if the schedule was never started — an App whose
// Start failed still gets Stopped, and blocking there would turn a startup
// error into a hang.
func (s *Scheduler) Stop() {
	s.once.Do(func() { close(s.stop) })
	if !s.started.Load() {
		return
	}
	<-s.done
}

func (s *Scheduler) loop() {
	defer close(s.done)

	for {
		// Re-read every cycle: both the time and whether the job runs at all
		// are settings someone may have just changed.
		enabled := s.settings.Bool(settings.BackupEnabled)
		wait := s.until(time.Now())

		if enabled {
			// The offset, not the zone abbreviation: Go renders Asia/Manila as
			// "PST" (Philippine Standard Time), which reads as US Pacific to
			// anyone scanning a log and is eight hours of confusion waiting to
			// happen.
			s.log.Info("next backup scheduled",
				"at", time.Now().Add(wait).In(s.settings.Location()).Format("2006-01-02 15:04:05 -07:00"),
				"timezone", s.settings.Location().String(),
				"in", wait.Round(time.Minute).String())
		}

		select {
		case <-s.stop:
			return
		case <-time.After(wait):
		}

		if !s.settings.Bool(settings.BackupEnabled) {
			// Checked again after waking: the setting may have been turned off
			// during the wait, and running anyway would ignore it for a day.
			s.log.Info("nightly backup is turned off; skipping")
			continue
		}
		s.RunNow(context.Background())
	}
}

// RunNow performs a backup immediately, logging the outcome. It is what both
// the scheduler and the "Backup now" action call, so there is one path and one
// log line shape whichever triggered it.
func (s *Scheduler) RunNow(ctx context.Context) (Result, error) {
	s.running.Lock()
	defer s.running.Unlock()

	result, err := Run(ctx, s.db, Options{
		Folder:    s.folder,
		Retention: s.settings.Int(settings.BackupRetention),
	})
	if err != nil {
		// This is the one background failure that genuinely needs someone to
		// notice, so it is logged at error level with the folder named: the
		// usual cause is the USB stick not being plugged in.
		s.log.Error("backup failed", "folder", s.folder, "error", err)
	} else {
		s.log.Info("backup written and verified", "backup", result)
	}
	return result, err
}

// until is how long to wait for the next scheduled run, in the station's
// timezone. Computing it from wall-clock time each cycle — rather than adding
// 24h to the last run — means a daylight-saving shift or a corrected system
// clock moves the backup to the right time rather than drifting it forever.
func (s *Scheduler) until(now time.Time) time.Duration {
	loc := s.settings.Location()
	hour, minute := s.settings.TimeOfDay(settings.BackupSchedule)

	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}

	wait := next.Sub(local)
	if wait < time.Second {
		// Guard against a zero or negative wait spinning the loop, which a
		// clock jumping backwards could otherwise produce.
		wait = time.Second
	}
	return wait
}
