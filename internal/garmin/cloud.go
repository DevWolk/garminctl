package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/deviceinfo"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Cloud retains legacy feature delegates while Connect owns its state.
type Cloud struct {
	*connect.Cloud
	svc *Service
}

// NewCloud borrows the existing Session and canonical feature-cache callback.
func NewCloud(svc *Service, client *gc.Client) *Cloud {
	var sess *watchsession.Session

	cfg := connect.Config{}

	if svc != nil {
		sess = svc.session
		cfg.Invalidated = func(_ *watchsession.Session, gen uint64) { svc.schemas.Reset(gen) }
	}

	return &Cloud{Cloud: connect.NewCloud(sess, client, cfg), svc: svc}
}

// NewJobCloud preserves epoch-bound groups through the same Manager.
func NewJobCloud(watch *Lazy, epoch ConnectionEpoch, client *gc.Client) *Cloud {
	cfg := connect.Config{Invalidated: func(sess *watchsession.Session, gen uint64) { watch.AppSchemaCacheFor(sess).Reset(gen) }}
	return &Cloud{Cloud: connect.NewJobCloud(watch.Manager(), epoch, client, cfg)}
}

func (c *Cloud) deviceXML(ctx context.Context) (*deviceinfo.DeviceXML, error) {
	return c.DeviceXML(ctx)
}

func (c *Cloud) pullQueued(ctx context.Context, kind string) ([]PulledFile, error) {
	return c.PullQueued(ctx, kind)
}

// CloudProfile preserves the Connect-owned public contract.
type CloudProfile = connect.CloudProfile

// CloudStatus preserves the Connect-owned public contract.
type CloudStatus = connect.CloudStatus

// CloudSetting preserves the Connect-owned public contract.
type CloudSetting = connect.CloudSetting

// CloudOption preserves the Connect-owned public contract.
type CloudOption = connect.CloudOption

// CloudApplyResult preserves the Connect-owned public contract.
type CloudApplyResult = connect.CloudApplyResult

// DeviceMessage preserves the Connect-owned public contract.
type DeviceMessage = connect.DeviceMessage

// PulledFile preserves the Connect-owned public contract.
type PulledFile = connect.PulledFile

// UploadedFile preserves the Connect-owned public contract.
type UploadedFile = connect.UploadedFile

// UploadResult preserves the Connect-owned public contract.
type UploadResult = connect.UploadResult

// Progress preserves the Connect-owned public contract.
type Progress = connect.Progress

// SyncResult preserves the Connect-owned public contract.
type SyncResult = connect.SyncResult
