package apiv2

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type NotificationIncomingService interface {
	ReceiveIncoming(context.Context, int, string, notifications.IncomingNotification) (notifications.IncomingResult, error)
}

type NotificationIncomingInput struct {
	Body struct {
		Source    string   `json:"source" minLength:"1" maxLength:"64" pattern:"^[a-z0-9][a-z0-9._-]*$"`
		EventID   string   `json:"event_id" minLength:"1" maxLength:"128"`
		Title     string   `json:"title" minLength:"1" maxLength:"200"`
		Message   string   `json:"message,omitempty" maxLength:"4000"`
		URL       string   `json:"url,omitempty" maxLength:"2048" format:"uri-reference" doc:"Optional server-relative destination; external origins are rejected."`
		ExpiresAt *Instant `json:"expires_at,omitempty" doc:"UTC expiry; suppresses admission and later channel attempts after this time. Already delivered messages cannot be recalled."`
	}
}

type NotificationIncomingOutput struct {
	Body NotificationIncomingResult
}

type NotificationIncomingResult struct {
	Status     string `json:"status" enum:"accepted,duplicate,disabled,expired"`
	DeliveryID ID     `json:"delivery_id,omitempty"`
}

type NotificationIncomingCapabilityOutput struct {
	Status       int
	ETag         string `header:"ETag"`
	CacheControl string `header:"Cache-Control"`
	Body         NotificationIncomingCapability
}

type NotificationIncomingCapability struct {
	Capability
	Available bool `json:"available"`
}

func (c NotificationIncomingCapability) capabilityState() string {
	return configuredCapabilityState(c.Available)
}

func registerNotificationIncoming(reg *Registry) {
	capability := notificationOperation(http.MethodGet, "/incoming/capabilities", "getIncomingNotificationCapabilities")
	capability.Summary = "Discover authenticated incoming notification support for the acting profile."
	Register(reg, capability, func(_ context.Context, _ *CapabilityInput) (*NotificationIncomingCapabilityOutput, error) {
		return &NotificationIncomingCapabilityOutput{Body: NotificationIncomingCapability{Available: reg.deps.NotificationIncoming != nil}}, nil
	})
	op := notificationOperation(http.MethodPost, "/incoming", "receiveIncomingNotification")
	op.RetrySafety = RetrySafetyDomainIdentity
	op.MaxBodyBytes = 32768
	op.Summary = "Create a notification for the authenticated account's verified profile. Replay the exact source/event ID and content; changed content conflicts."
	Register(reg, op, func(ctx context.Context, in *NotificationIncomingInput) (*NotificationIncomingOutput, error) {
		if reg.deps.NotificationIncoming == nil {
			return nil, unavailable("incoming notifications")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		body := in.Body
		event := notifications.IncomingNotification{Source: body.Source, EventID: body.EventID, Title: body.Title, Message: body.Message, URL: body.URL}
		if body.ExpiresAt != nil {
			stamp := body.ExpiresAt.UnixMilli()
			event.ExpiresAt = &stamp
		}
		result, err := reg.deps.NotificationIncoming.ReceiveIncoming(ctx, user, profile, event)
		if errors.Is(err, notifications.ErrIncomingInvalid) {
			return nil, NewProblem(TypeValidationFailed, "Invalid notification content or destination.")
		}
		if errors.Is(err, notifications.ErrIncomingConflict) {
			return nil, NewProblem(TypeConflict, "This source/event ID already names a notification with different content.")
		}
		if err != nil {
			return nil, serviceProblem(err)
		}
		return &NotificationIncomingOutput{Body: NotificationIncomingResult{Status: result.Status, DeliveryID: ID(result.DeliveryID)}}, nil
	})
}
