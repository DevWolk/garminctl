//go:build desktop

package desktop

import "context"

// windowHost is the native boundary used by the frontend service. The backend
// retains watch/job ownership; host methods only affect the window or dialogs.
type windowHost interface {
	Emit(string)
	Quit()
	OpenExternal(string) error
	SaveFile(context.Context, string) (string, error)
}
