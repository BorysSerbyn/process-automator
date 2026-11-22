package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/trust"
)

// TrustStore records which routines are trusted with what.
func (a *App) TrustStore() trust.Store {
	return trust.Store{Path: a.Layout.Trust()}
}

// trustedAtFirstUse are the kinds InitTrust trusts as they are: what existed
// before trust did.
var trustedAtFirstUse = []Kind{KindLLM}

// Approval is what trusting x approves, as it is now: an LLM routine's
// surface, a program's executable and description file.
func (a *App) Approval(x Runnable) (trust.Approval, error) { return x.approval(a) }

// Trust records x as trusted with what it is now.
func (a *App) Trust(x Runnable) error {
	ap, err := x.approval(a)
	if err != nil {
		return err
	}
	return a.TrustStore().Trust(map[string]trust.Approval{x.Name(): ap}, time.Now())
}

// Untrusted says why x may not run for real and how to fix it, or "" when it may.
func (a *App) Untrusted(x Runnable) (string, error) {
	why, changed, err := a.TrustProblem(x)
	switch {
	case err != nil || why == "":
		return why, err
	case changed:
		return fmt.Sprintf("%s: review it with `%s`", why, TrustCommand(x)), nil
	default:
		return fmt.Sprintf("%s: review what it can reach with `%s`", why, TrustCommand(x)), nil
	}
}

// TrustCommand is the command that reviews and trusts x.
func TrustCommand(x Runnable) string {
	if _, ok := x.(*hook); ok {
		return product.Command + " hooks trust " + x.Name()
	}
	return product.Command + " routines trust " + x.Name()
}

// TrustProblem says in plain words why x may not run for real ("" when it
// may); changed is true when it was trusted before. It only reads: before
// trust is first used (no trust file), every LLM routine counts as trusted,
// as first use will make it (InitTrust, called by runs, the scheduler and
// edits).
func (a *App) TrustProblem(x Runnable) (why string, changed bool, err error) {
	f, initialized, err := a.TrustStore().Load()
	if err != nil {
		return "", false, err
	}
	if _, isHook := x.(*hook); !initialized && !isHook && slices.Contains(trustedAtFirstUse, x.Kind()) {
		return "", false, nil
	}
	rec, ok := f.Routines[x.Name()]
	if !ok {
		return "not trusted yet", false, nil
	}
	cur, err := x.approval(a)
	if err != nil {
		return "", false, err
	}
	if rec.Approves(cur) {
		return "", false, nil
	}
	return fmt.Sprintf("changed since you trusted it (%s)", strings.Join(rec.ChangesTo(cur), "; ")), true, nil
}

// RunConfig is the settings x has whatever its kind.
func (a *App) RunConfig(x Runnable) (RunConfig, error) { return x.runConfig(a) }

// recordTrustedPermissions records the permissions that what was trusted
// before permissions were declared needs now: it already needed them when
// it was trusted, so turning them into a change to review would only ask
// me again about what I approved. Each record is filled once; ones whose
// routine, hook or watcher doesn't load wait until it does.
func (a *App) recordTrustedPermissions(f *trust.File) error {
	var unrecorded []string
	for name, rec := range f.Routines {
		if rec.Unrecorded() {
			unrecorded = append(unrecorded, name)
		}
	}
	if len(unrecorded) == 0 {
		return nil
	}
	current := map[string][]string{}
	all, _, err := a.All()
	if err != nil {
		return err
	}
	hooks, _, err := a.Hooks()
	if err != nil {
		return err
	}
	for _, x := range append(all, hooks...) {
		if slices.Contains(unrecorded, x.Name()) {
			if ap, err := x.approval(a); err == nil {
				current[x.Name()] = ap.Permissions
			}
		}
	}
	ws, _, err := a.Watchers()
	if err != nil {
		return err
	}
	for _, w := range ws {
		if slices.Contains(unrecorded, w.Name) {
			current[w.Name] = w.Program.Permissions
		}
	}
	if len(current) == 0 {
		return nil
	}
	return a.TrustStore().RecordPermissions(current)
}
