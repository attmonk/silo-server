package notifications

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type notificationNotice struct {
	Source    string `json:"source"`
	EventID   string `json:"event_id"`
	Title     string `json:"title"`
	Message   string `json:"message,omitempty"`
	URL       string `json:"url,omitempty"`
	ExpiresAt *int64 `json:"expires_at,omitempty"`
}

func validNoticeURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") &&
		u.Scheme == "" && u.Host == "" && u.User == nil && u.Opaque == "" &&
		!strings.HasPrefix(u.Path, "//") && !strings.ContainsAny(raw, "\\") &&
		!strings.ContainsAny(u.Path, "\\") && !strings.ContainsFunc(u.Path, unicode.IsControl)
}

func noticeFlags(row DeliveryRow) notificationNotice {
	var flags notificationNotice
	if row.Type == DeliveryTypePremiere || row.Type == DeliveryTypeIncoming {
		_ = json.Unmarshal(row.ReasonFlags, &flags)
	}
	return flags
}

func deliveryExpired(row DeliveryRow, now time.Time) bool {
	flags := noticeFlags(row)
	return flags.ExpiresAt != nil && now.UnixMilli() >= *flags.ExpiresAt
}
