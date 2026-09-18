package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// settingView is one setting as the UI shows it: the value, plus enough
// description to render an editor without hard-coding a form per setting.
type settingView struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Kind is the sentence shown under the field; KindSlug is the token the
	// screen branches on. Two fields on purpose — see settings.Kind.Slug.
	Kind     string `json:"kind"`
	KindSlug string `json:"kind_slug"`
	Default  string `json:"default"`
	Doc      string `json:"doc"`

	// Options is what a choice setting may be set to, so the screen can offer a
	// list rather than a text box. Absent for every other kind.
	Options []string `json:"options,omitempty"`
}

// view is one setting as the API presents it. It exists so the list and the
// response to an update cannot describe the same setting differently.
func view(def settings.Def, value string) settingView {
	return settingView{
		Key:      def.Key,
		Value:    value,
		Kind:     def.Kind.String(),
		KindSlug: def.Kind.Slug(),
		Default:  def.Default,
		Doc:      def.Doc,
		Options:  def.Options,
	}
}

func handleListSettings(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views := make([]settingView, 0, len(settings.All))
		for _, def := range settings.All {
			views = append(views, view(def, d.Settings.Raw(def)))
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": views})
	}
}

type updateSettingRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// handleUpdateSetting changes one setting.
//
// Manager-only: the Ref. No. start and the backup schedule are not things an
// encoder should be able to change mid-shift, and both of them have
// consequences that are hard to see immediately.
//
// The write goes through the route wrapper's transaction, so the change and its
// audit entry commit together — and the cache reload happens after the commit,
// which is what makes the new value take effect without a restart.
func handleUpdateSetting(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var req updateSettingRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		def, ok := settings.Lookup(req.Key)
		if !ok {
			// Unknown keys are refused rather than stored. A settings table
			// that accepts anything becomes a junk drawer, and a misspelled
			// key would be silently ignored by the code that wanted to read it.
			return Change{}, badRequest("%q is not a setting this system has", req.Key)
		}
		if err := settings.Validate(def, req.Value); err != nil {
			return Change{}, badRequest("%s", err.Error())
		}
		if def.Key == settings.LANAccess.Key && !isHostRequest(r) {
			// The lockout this prevents is not hypothetical: a manager at a
			// counter PC who switches LAN access off loses the app mid-click,
			// along with the only screen that could switch it back on. Holding
			// the switch at the host machine means the person flipping it is
			// standing at the one machine that is certain to still work
			// afterwards.
			return Change{}, forbidden(
				"network access can only be changed at the host machine itself")
		}

		before := d.Settings.Raw(def)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			def.Key, req.Value, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return Change{}, err
		}

		return Change{
			EntityID:    def.Key,
			Before:      map[string]string{"value": before},
			After:       map[string]string{"value": req.Value},
			Body:        view(def, req.Value),
			AfterCommit: afterSettingChange(d, def),
		}, nil
	}
}

// afterSettingChange is the work a changed setting implies once it is durable.
//
// Reloading the cache is what makes every setting take effect without a
// restart. LAN access needs one thing more: the name on the network has to
// follow the switch, so that turning access off stops advertising a server that
// will refuse, and turning it back on publishes it again.
func afterSettingChange(d Deps, def settings.Def) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := d.Settings.Reload(ctx); err != nil {
			return err
		}
		if def.Key != settings.LANAccess.Key || d.Discovery == nil {
			return nil
		}
		return d.Discovery.SetPublished(d.Settings.Bool(settings.LANAccess))
	}
}
