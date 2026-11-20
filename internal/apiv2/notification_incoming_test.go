package apiv2

import (
	"context"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fixtureIncomingNotifications struct{ calls int }

func (f *fixtureIncomingNotifications) ReceiveIncoming(_ context.Context, user int, profile string, _ notifications.IncomingNotification) (notifications.IncomingResult, error) {
	f.calls++
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
