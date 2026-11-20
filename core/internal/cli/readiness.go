package cli

import "github.com/BorysSerbyn/process-automator/core/internal/engine"

// signedIn is, for each server that signs in, whether it is.
func signedIn(a *engine.App) map[string]bool {
	out := map[string]bool{}
	if rows, err := authStatuses(a); err == nil {
		for _, row := range rows {
			out[row.Server] = row.SignedIn
		}
	}
	return out
}
