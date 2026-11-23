package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
	"github.com/BorysSerbyn/process-automator/core/internal/runstatus"
	"github.com/BorysSerbyn/process-automator/core/internal/sysenv"
	"github.com/BorysSerbyn/process-automator/core/internal/update"
)

// autoUpdater is the scheduler's part in updates: on each pass it checks
// for a newer release when it's time (every update.CheckEvery), and, while
// automatic updates are on, starts installing it at a quiet moment
// (update.Decide). The install runs detached, as one started from the menu
// does, and restarts the scheduler when it's done.
type autoUpdater struct {
	// awakeSince is when the scheduler started or the computer last woke.
	awakeSince time.Time
	// checking is true while a check runs (in the background: it reaches
	// the network).
	checking atomic.Bool
}

// autoUpdates is whether the scheduler updates the app (tests turn it off:
// they never reach GitHub or install).
var autoUpdates = true

// checkTimeout bounds a check the scheduler starts.
const checkTimeout = 2 * time.Minute

func newAutoUpdater(now time.Time) *autoUpdater { return &autoUpdater{awakeSince: now} }

// woke records the computer waking.
func (u *autoUpdater) woke(now time.Time) { u.awakeSince = now }

// pass does what automatic updates call for now.
func (u *autoUpdater) pass(ctx context.Context, a *App, now time.Time) {
	if !autoUpdates {
		return
	}
	path := a.Layout.UpdateAuto()
	st, err := update.LoadAuto(path)
	if err != nil {
		logf("update: %v", err)
		return
	}
	plan := a.decideAuto(st, now, u.awakeSince)
	if plan.Check && u.checking.CompareAndSwap(false, true) {
		go func() {
			defer u.checking.Store(false)
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			if p, err := a.PlanUpdate(cctx); err != nil {
				logf("update: checking for a newer release: %v", err)
			} else if update.Newer(product.Version, p.Tag) {
				logf("update: %s is available (this is %s)", p.Tag, product.Version)
			}
		}()
	}
	if plan.Waiting != st.Waiting {
		if err := update.RecordWaiting(path, plan.Waiting); err != nil {
			logf("update: %v", err)
		}
	}
	if plan.Install == "" {
		return
	}
	logf("update: installing %s automatically (this is %s)", plan.Install, product.Version)
	if err := update.RecordTry(path, plan.Install, now); err != nil {
		logf("update: %v", err)
		return
	}
	if err := a.StartDetachedUpdate(true); err != nil {
		logf("update: couldn't start installing %s: %v", plan.Install, err)
		a.RecordUpdate("Couldn't update to "+plan.Install, err.Error()+"\n"+product.Name+" "+product.Version+" keeps working.")
	}
}

// decideAuto is update.Decide with what the app knows now: the setting,
// whether this install can be updated, whether one is installing, and
// what's running or due, from run status.
func (a *App) decideAuto(st update.AutoState, now, awakeSince time.Time) update.AutoPlan {
	f := update.Facts{
		Now: now, Current: product.Version, Blocked: autoBlocked(), AwakeSince: awakeSince,
		Installing: update.Installing(a.Layout.UpdateLock()),
		FullyAwake: sysenv.FullyAwake, When: runstatus.When,
	}
	if vals, err := a.Settings(); err == nil {
		f.Enabled = vals.Bool(AutoUpdateSetting)
	}
	if s, err := runstatus.Load(a.Layout.RunStatus()); err == nil {
		f.Busy, f.Next, f.NextName = activity(s, now)
	} else {
		f.Busy = "run status can't be read"
	}
	return update.Decide(st, f)
}

// activity is what's running or waiting its turn, in words ("" when
// nothing is), and the soonest scheduled run (whose).
// A run stuck past its deadline isn't running any more.
func activity(s *runstatus.Status, now time.Time) (busy string, next *time.Time, nextName string) {
	for _, list := range [][]runstatus.Routine{s.Routines, s.Hooks} {
		for _, r := range list {
			if run := r.LastRun; run != nil && busy == "" && !runstatus.Stuck(*run, now) {
				switch run.Outcome {
				case runstatus.OutcomeRunning:
					busy = r.Name + " is running"
				case runstatus.OutcomeQueued:
					busy = r.Name + " is waiting its turn"
				}
			}
			if r.Enabled && r.NextRunAt != nil && (next == nil || r.NextRunAt.Before(*next)) {
				next, nextName = r.NextRunAt, r.Name
			}
		}
	}
	return busy, next, nextName
}
