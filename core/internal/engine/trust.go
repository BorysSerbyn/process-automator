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
		return fmt.Sprintf("%s: review it with `"+product.Command+" routines trust %s`", why, x.Name()), nil
	default:
		return fmt.Sprintf("%s: review what it can reach with `"+product.Command+" routines trust %s`", why, x.Name()), nil
	}
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
	if !initialized && slices.Contains(trustedAtFirstUse, x.Kind()) {
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
	if cur.Equal(rec.Approval) {
		return "", false, nil
	}
	return fmt.Sprintf("changed since you trusted it (%s)", strings.Join(cur.Changes(rec.Approval), "; ")), true, nil
}

// RunConfig is the settings x has whatever its kind.
func (a *App) RunConfig(x Runnable) (RunConfig, error) { return x.runConfig(a) }
