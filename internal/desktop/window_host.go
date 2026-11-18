//go:build desktop

package desktop

import "context"

// windowHost is the native boundary used by the frontend service. The backend
// retains watch/job ownership; host methods only affect the window or dialogs.
type windowHost interface {
	Emit(string)
	// Reveal shows the main window if it is not on screen.
	Reveal()
	Quit()
	OpenExternal(string) error
	SaveFile(context.Context, string) (string, error)
	// ConfirmReplace asks before an existing file chosen in SaveFile is replaced,
	// unless the native Save dialog already did.
	ConfirmReplace(context.Context, string) (bool, error)
}
