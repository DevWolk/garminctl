package connect

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/gc"
)

// StatusAction runs the existing Status workflow.
type StatusAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a StatusAction) Run(ctx context.Context) *CloudStatus { return a.Cloud.Status(ctx) }

// SettingsAction runs the existing Settings workflow.
type SettingsAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a SettingsAction) Run(ctx context.Context) ([]CloudSetting, error) {
	return a.Cloud.Settings(ctx)
}

// ApplySettingsAction runs the existing SetSettings workflow.
type ApplySettingsAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a ApplySettingsAction) Run(ctx context.Context, changes map[string]any) (*CloudApplyResult, error) {
	return a.Cloud.SetSettings(ctx, changes)
}

// MessagesAction runs the existing Messages workflow.
type MessagesAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a MessagesAction) Run(ctx context.Context) ([]DeviceMessage, error) {
	return a.Cloud.Messages(ctx)
}

// PullAction runs the existing Pull workflow.
type PullAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a PullAction) Run(ctx context.Context, match func(DeviceMessage) bool) ([]PulledFile, error) {
	return a.Cloud.Pull(ctx, match)
}

// UploadAction runs the existing Upload workflow.
type UploadAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a UploadAction) Run(ctx context.Context, progress Progress) (*UploadResult, error) {
	return a.Cloud.Upload(ctx, progress)
}

// SyncAction runs the existing Sync workflow.
type SyncAction struct{ Cloud *Cloud }

// Run preserves the caller scope and existing result/error policy.
func (a SyncAction) Run(ctx context.Context, up, down bool, progress Progress) (*SyncResult, error) {
	return a.Cloud.Sync(ctx, up, down, progress)
}

// LoginAction starts the account client's existing SSO/MFA workflow.
type LoginAction struct{ Client *gc.Client }

// Run preserves token/challenge persistence and client error policy.
func (a LoginAction) Run(ctx context.Context, email, password string) (*gc.LoginResult, error) {
	return a.Client.Login(ctx, email, password)
}

// MFAAction completes the current persisted challenge.
type MFAAction struct{ Client *gc.Client }

// Run leaves code validation and account locking with gc.
func (a MFAAction) Run(ctx context.Context, code string) error { return a.Client.ResumeMFA(ctx, code) }

// LogoutAction clears the account client's owned state.
type LogoutAction struct {
	Client *gc.Client
	// ForgetCopies deletes copies of the saved session that the entry point
	// keeps outside the account store, such as application data backups. Nil
	// when it keeps none.
	ForgetCopies func(context.Context) error
}

// Run retains account-store protection and cleanup errors. Copies are deleted
// even when the store itself could not be cleared, and both failures are reported.
func (a LogoutAction) Run(ctx context.Context) error {
	err := a.Client.Logout()
	if a.ForgetCopies != nil {
		err = errors.Join(err, a.ForgetCopies(ctx))
	}

	return err
}
