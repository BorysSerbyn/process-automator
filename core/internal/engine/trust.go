package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/routines"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// TrustStore records which routines are trusted with what.
func (a *App) TrustStore() trust.Store {
	return trust.Store{Path: a.Layout.Trust()}
}

// SurfaceOf is what a routine can reach, with its effective settings.
func SurfaceOf(r routines.Routine, c RoutineConfig) trust.Surface {
	return trust.Surface{Servers: r.Servers, Tools: r.Tools, BuiltinTools: c.BuiltinTools, Folder: c.Folder, Plugins: r.Plugins}
}

// InitTrust trusts, the first time trust is used, every routine installed
// then: routines from before trust existed keep running. Edits, real runs and
// the scheduler call it first, so a routine made afterwards is never trusted
// this way; read-only commands don't (TrustProblem reads as if it had run).
func (a *App) InitTrust() error {
	if _, ok, err := a.TrustStore().Load(); ok || err != nil {
		return err
	}
	set, err := routines.Load(a.Layout.Routines())
	if err != nil {
		return err
	}
	current := map[string]trust.Surface{}
	for _, r := range set.Routines {
		if c, err := a.RoutineConfig(r); err == nil {
			current[r.Name] = SurfaceOf(r, c)
		}
	}
	return a.TrustStore().Init(current, time.Now())
}

// Untrusted says why r may not run for real and how to fix it, or "" when it may.
func (a *App) Untrusted(r routines.Routine, c RoutineConfig) (string, error) {
	why, changed, err := a.TrustProblem(r, c)
	switch {
	case err != nil || why == "":
		return why, err
	case changed:
		return fmt.Sprintf("%s: review it with `"+product.Command+" routines trust %s`", why, r.Name), nil
	default:
		return fmt.Sprintf("%s: review what it can reach with `"+product.Command+" routines trust %s`", why, r.Name), nil
	}
}

// TrustProblem says in plain words why r may not run for real ("" when it
// may); changed is true when it was trusted before. It only reads: before
// trust is first used (no trust file), every routine counts as trusted, as
// first use will make it (InitTrust, called by runs, the scheduler and edits).
func (a *App) TrustProblem(r routines.Routine, c RoutineConfig) (why string, changed bool, err error) {
	f, initialized, err := a.TrustStore().Load()
	if err != nil || !initialized {
		return "", false, err
	}
	rec, ok := f.Routines[r.Name]
	if !ok {
		return "not trusted yet", false, nil
	}
	cur := SurfaceOf(r, c)
	if cur.Equal(rec.Surface) {
		return "", false, nil
	}
	return fmt.Sprintf("changed since you trusted it (%s)", strings.Join(cur.Changes(rec.Surface), "; ")), true, nil
}
