package ui

import "time"

// PendingTimeout is how long a control waits for its state to show before
// it gives up and shows things as they are (0: it doesn't time out; tests).
var PendingTimeout = 30 * time.Second

// pendingAction is a control's action from the moment it's tapped until
// what it waits for shows: Run Now until run status says the routine runs
// (or a run that started has ended already), Stop until it no longer runs,
// Pause until it's paused. Meanwhile its button says what it's doing
// ("Starting…") and every control it guards is disabled, so it can't be
// tapped twice. It ends when its state shows (settle, on each run status
// and each read of the routine), when the command fails (its error shows
// as before), or after PendingTimeout. It mirrors automator-ui's
// PendingAction.
type pendingAction struct {
	async  Async
	redraw func()

	action, label string
	until         func() bool
	token         int
}

func newPendingAction(async Async, redraw func()) *pendingAction {
	return &pendingAction{async: async, redraw: redraw}
}

// busy is whether an action is pending.
func (p *pendingAction) busy() bool { return p.action != "" }

// title is a control's title, or what it's doing while one of its actions
// is pending.
func (p *pendingAction) title(title string, actions ...string) string {
	for _, a := range actions {
		if p.action == a {
			return p.label
		}
	}
	return title
}

// run runs work (`action`, its button saying `label` meanwhile), then done
// with its error; once it returns, the action stays pending until `until`
// holds (nil: it's over then).
func (p *pendingAction) run(action, label string, until func() bool, work func() error, done func(error)) {
	p.token++
	mine := p.token
	p.action, p.label, p.until = action, label, nil
	p.redraw()
	p.async(func() func() {
		err := work()
		return func() {
			if mine == p.token {
				if err != nil || until == nil {
					p.end()
				} else {
					p.until = until
					p.settle()
					if p.busy() {
						p.expire(mine)
					}
				}
			}
			done(err)
			p.redraw()
		}
	})
}

// expire ends the action after PendingTimeout, if it's still the one pending.
func (p *pendingAction) expire(mine int) {
	if PendingTimeout <= 0 {
		return
	}
	p.async(func() func() {
		time.Sleep(PendingTimeout)
		return func() {
			if p.token == mine {
				p.end()
				p.redraw()
			}
		}
	})
}

// settle ends the action pending when what it waits for shows.
func (p *pendingAction) settle() {
	if p.until != nil && p.until() {
		p.end()
	}
}

func (p *pendingAction) end() {
	p.action, p.label, p.until = "", "", nil
	p.token++
}
