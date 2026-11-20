package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fixtureIncomingNotifications struct {
	calls int
	event notifications.IncomingNotification
	err   error
}

func (f *fixtureIncomingNotifications) ReceiveIncoming(_ context.Context, user int, profile string, event notifications.IncomingNotification) (notifications.IncomingResult, error) {
	f.calls++
	f.event = event
	if f.err != nil {
		return notifications.IncomingResult{}, f.err
	}
	if user != 1 || profile != "p-owner" {
		return notifications.IncomingResult{}, notifications.ErrIncomingInvalid
	}
	return notifications.IncomingResult{Status: "accepted", DeliveryID: notificationFixtureID}, nil
}

func TestIncomingNotificationAuthorityAndShape(t *testing.T) {
	f := new(fixtureIncomingNotifications)
	deps := pilotDeps(nil, nil)
	deps.NotificationIncoming = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/incoming"
	body := `{"source":"integration","event_id":"one","title":"Reminder"}`
	for _, tc := range []struct {
		headers map[string]string
		problem ProblemType
	}{
		{nil, TypeAuthenticationRequired},
		{bearer(memberToken), TypeValidationFailed},
		{with(bearer(memberToken), "X-Profile-Id", "p-foreign"), TypeNotFound},
	} {
		requireProblem(t, do(t, h, http.MethodPost, path, body, tc.headers), tc.problem)
	}
	if f.calls != 0 {
		t.Fatal("unauthorized admission reached the notification system")
	}
	// Recipient identity must never be accepted from the JSON body.
	requireProblem(t, do(t, h, http.MethodPost, path, `{"source":"integration","event_id":"one","title":"Reminder","user_id":2,"profile_id":"p-foreign"}`, profileOwner()), TypeValidationFailed)
	requireProblem(t, do(t, h, http.MethodPost, path, `{"source":"integration","event_id":"one","title":"Reminder","expires_at":"not-a-time"}`, profileOwner()), TypeValidationFailed)
	if f.calls != 0 {
		t.Fatal("malformed admission reached the notification system")
	}
}

func TestIncomingNotificationUsesVerifiedProfile(t *testing.T) {
	f := new(fixtureIncomingNotifications)
	deps := pilotDeps(nil, nil)
	deps.NotificationIncoming = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/incoming"
	body := `{"source":"integration","event_id":"one","title":"Reminder","message":"Your collection is ready","url":"/notifications","expires_at":"2030-01-02T03:04:05Z"}`
	response := do(t, h, http.MethodPost, path, body, profileOwner())
	if response.Code != http.StatusOK {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	var result NotificationIncomingResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	wantStamp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	if f.calls != 1 || f.event.Source != "integration" || f.event.EventID != "one" || f.event.Title != "Reminder" || f.event.Message != "Your collection is ready" || f.event.URL != "/notifications" || f.event.ExpiresAt == nil || *f.event.ExpiresAt != wantStamp || result.Status != "accepted" || result.DeliveryID != notificationFixtureID {
		t.Fatalf("event=%+v result=%+v calls=%d", f.event, result, f.calls)
	}
	for _, tc := range []struct {
		err     error
		problem ProblemType
	}{
		{notifications.ErrIncomingConflict, TypeConflict},
		{notifications.ErrIncomingInvalid, TypeValidationFailed},
	} {
		f.err = errors.Join(errors.New("admission failed"), tc.err)
		requireProblem(t, do(t, h, http.MethodPost, path, body, profileOwner()), tc.problem)
	}
}
