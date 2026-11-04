package workouts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/DevWolk/garminctl/internal/deviceinfo"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Account is the workout API protocol supplied by the shared Connect client.
type Account interface {
	GetJSON(context.Context, string, any) error
	SendJSON(context.Context, string, string, any, any) error
}

// Delivery borrows the caller's watch scope and pulls only workout messages.
// Queue routing, retry timing, acknowledgement and app-cache policy stay with
// Connect; the implementation must not acquire an already-held lease again.
type Delivery interface {
	DeviceXML(context.Context) (*deviceinfo.DeviceXML, error)
	PullQueued(context.Context) ([]PulledFile, error)
}

// WriteLocalAction stages a type-5 workout without using Connect.
type WriteLocalAction struct{}

// Run retains required XML metadata and one unique NewFiles path per workout.
func (WriteLocalAction) Run(ctx context.Context, sess *watchsession.Session, spec *WorkoutSpec) (string, error) {
	dx, err := sess.DeviceXML(ctx)
	if err != nil {
		return "", err
	}

	data, err := spec.FIT(dx.UnitID, time.Now())
	if err != nil {
		return "", err
	}

	p := watchsession.PathNewFiles + "/" + watchsession.NewFileID() + ".FIT"
	if err := sess.WriteFile(ctx, p, data); err != nil {
		return "", err
	}

	return p, nil
}

// ListAction preserves the existing Workouts scenario.
type ListAction struct{ Client Account }

// Run performs the workout operation with the caller context.
func (a ListAction) Run(ctx context.Context, limit int) ([]CloudWorkout, error) {
	if limit <= 0 {
		limit = 50
	}

	var raw []map[string]any
	if err := a.Client.GetJSON(ctx, fmt.Sprintf("/workout-service/workouts?start=0&limit=%d&myWorkoutsOnly=true", limit), &raw); err != nil {
		return nil, err
	}

	out := make([]CloudWorkout, 0, len(raw))
	for _, m := range raw {
		w := CloudWorkout{ID: int64(num(m["workoutId"])), Name: fmt.Sprint(m["workoutName"]), Duration: asInt(m, "estimatedDurationInSecs")}
		if st, ok := m["sportType"].(map[string]any); ok {
			w.Sport, _ = st["sportTypeKey"].(string)
		}

		if u, ok := m["updatedDate"].(string); ok && len(u) >= 10 {
			w.Updated = u[:10]
		}

		out = append(out, w)
	}

	return out, nil
}

// CreateAction preserves the existing CreateWorkout scenario.
type CreateAction struct{ Client Account }

// Run performs the workout operation with the caller context.
func (a CreateAction) Run(ctx context.Context, spec *WorkoutSpec) (*CloudWorkout, error) {
	doc, err := spec.ConnectJSON()
	if err != nil {
		return nil, err
	}

	var resp map[string]any
	if err := a.Client.SendJSON(ctx, http.MethodPost, "/workout-service/workout", doc, &resp); err != nil {
		return nil, err
	}

	w := &CloudWorkout{ID: int64(num(resp["workoutId"])), Name: spec.Name, Sport: spec.Sport, Duration: asInt(resp, "estimatedDurationInSecs")}
	if w.ID == 0 {
		return nil, errors.New("no workout id in the Garmin Connect answer")
	}

	return w, nil
}

// SendAction preserves the existing SendWorkout scenario.
type SendAction struct {
	Client Account
	Queue  Delivery
}

// Run performs the workout operation with the caller context.
func (a SendAction) Run(ctx context.Context, id int64, name string) ([]PulledFile, error) {
	dx, err := a.Queue.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	body := []map[string]any{{"deviceId": dx.UnitID, "messageUrl": fmt.Sprintf("workout-service/workout/FIT/%d", id), "messageType": "workouts", "groupName": nil, "messageName": name, "priority": 1, "fileType": "FIT", "metaDataId": id}}
	if err := a.Client.SendJSON(ctx, http.MethodPost, "/device-service/devicemessage/messages", body, nil); err != nil {
		return nil, err
	}

	pulled, err := a.Queue.PullQueued(ctx)
	if err != nil {
		return nil, err
	}

	return pulled, nil
}

// DeleteAction preserves the existing DeleteWorkout scenario.
type DeleteAction struct{ Client Account }

// Run performs the workout operation with the caller context.
func (a DeleteAction) Run(ctx context.Context, id int64) error {
	return a.Client.SendJSON(ctx, http.MethodDelete, fmt.Sprintf("/workout-service/workout/%d", id), nil, nil)
}
