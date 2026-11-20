package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

const DeliveryTypeIncoming = "external.notification"

var (
	ErrIncomingInvalid  = errors.New("invalid incoming notification")
	ErrIncomingConflict = errors.New("notification event already exists with different content")
	errIncomingHandled  = errors.New("incoming notification already handled")
	incomingSource      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// IncomingNotification is plain text supplied by an authenticated account.
// Recipient identity comes from the request's verified profile, never this body.
type IncomingNotification struct {
	Source    string `json:"source"`
	EventID   string `json:"event_id"`
	Title     string `json:"title"`
	Message   string `json:"message,omitempty"`
	URL       string `json:"url,omitempty"`
	ExpiresAt *int64 `json:"expires_at,omitempty"`
}

type IncomingResult struct {
	Status     string `json:"status"`
	DeliveryID string `json:"delivery_id,omitempty"`
}

func (in IncomingNotification) validate() error {
	validText := func(s string, limit int, multiline bool) bool {
		return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit &&
			!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && (!multiline || (r != '\n' && r != '\t')) })
	}
	if !incomingSource.MatchString(in.Source) || strings.TrimSpace(in.EventID) == "" ||
		!validText(in.EventID, 128, false) || strings.TrimSpace(in.Title) == "" ||
		!validText(in.Title, 200, false) || !validText(in.Message, 4000, true) ||
		!validText(in.URL, 2048, false) || !validIncomingURL(in.URL) ||
		(in.ExpiresAt != nil && *in.ExpiresAt <= 0) {
		return ErrIncomingInvalid
	}
	return nil
}

// Decode the path too: encoded slashes/backslashes must not become an origin.
func validIncomingURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") &&
		u.Scheme == "" && u.Host == "" && u.User == nil && u.Opaque == "" &&
		!strings.HasPrefix(u.Path, "//") && !strings.ContainsAny(raw, "\\") &&
		!strings.ContainsAny(u.Path, "\\") && !strings.ContainsFunc(u.Path, unicode.IsControl)
}

func incomingFlags(row DeliveryRow) IncomingNotification {
	var flags IncomingNotification
	if row.Type == DeliveryTypeIncoming || row.Type == DeliveryTypePremiere {
		_ = json.Unmarshal(row.ReasonFlags, &flags)
	}
	return flags
}

func deliveryExpired(row DeliveryRow, now time.Time) bool {
	flags := incomingFlags(row)
	return flags.ExpiresAt != nil && now.UnixMilli() >= *flags.ExpiresAt
}

// ReceiveIncoming uses the ordinary inbox/outbox transaction. Replays bind
// account, profile, source and event ID; existing deliveries remain immutable.
// Outbound webhooks are excluded to prevent webhook feedback loops.
func (s *System) ReceiveIncoming(ctx context.Context, userID int, profileID string, in IncomingNotification) (IncomingResult, error) {
	if err := in.validate(); err != nil {
		return IncomingResult{}, err
	}
	raw, _ := json.Marshal(in)
	uiEnabled := s.Settings.UIEnabled(ctx)
	result := IncomingResult{Status: "accepted"}
	inserted, err := s.DispatchOperational(ctx, Delivery{
		ID: ulid.Make().String(), UserID: userID, ProfileID: profileID,
		Type: DeliveryTypeIncoming, ReasonFlags: raw,
	}, OperationalDispatch{admit: func(ctx context.Context, tx pgx.Tx) error {
		key, _ := json.Marshal([]any{userID, profileID, in.Source, in.EventID})
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, string(key)); err != nil {
			return err
		}
		var id string
		var same bool
		err := tx.QueryRow(ctx, `SELECT id, reason_flags = $5::jsonb FROM notification_deliveries
			WHERE user_id=$1 AND profile_id=$2 AND type='external.notification'
			AND reason_flags->>'source'=$3 AND reason_flags->>'event_id'=$4`,
			userID, profileID, in.Source, in.EventID, raw).Scan(&id, &same)
		if err == nil {
			if !same {
				return ErrIncomingConflict
			}
			result = IncomingResult{Status: "duplicate", DeliveryID: id}
			return errIncomingHandled
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.ExpiresAt != nil && time.Now().UnixMilli() >= *in.ExpiresAt {
			result.Status = "expired"
			return errIncomingHandled
		}
		prefs, err := s.Preferences.GetMany(ctx, tx, []profileKey{{userID, profileID}})
		if err != nil {
			return err
		}
		if !uiEnabled || !prefs[profileKey{userID, profileID}].Enabled {
			result.Status = "disabled"
			return errIncomingHandled
		}
		return nil
	}})
	if errors.Is(err, errIncomingHandled) {
		return result, nil
	}
	if err != nil {
		return IncomingResult{}, fmt.Errorf("receive incoming notification: %w", err)
	}
	if inserted == nil {
		return IncomingResult{}, errors.New("incoming notification was not inserted")
	}
	result.DeliveryID = inserted.ID
	return result, nil
}
