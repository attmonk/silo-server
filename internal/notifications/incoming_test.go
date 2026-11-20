package notifications

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercises the real migrated schema, concurrent admission, durable channel
// enqueue, and process reconstruction. No notification workers are started.
func TestIncomingNotificationPersistenceAndIsolation(t *testing.T) {
	p := inboxPageDB(t)
	ctx := t.Context()
	if _, err := p.Exec(ctx, `CREATE TABLE push_devices (LIKE public.push_devices INCLUDING ALL);
		CREATE TABLE push_delivery_attempts (LIKE public.push_delivery_attempts INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	for _, account := range []int{1, 2} {
		if _, err := p.Exec(ctx, `INSERT INTO push_devices
			(id,user_id,profile_id,device_id,platform,provider,apns_environment,apns_topic,apns_token_ciphertext,apns_token_hash,server_device_id)
			VALUES ('device-' || $1::integer::text, $1, 'default', 'device-' || $1::integer::text, 'apple','silo_relay','production','org.siloserver.silo','ciphertext','hash-' || $1::integer::text,'server-' || $1::integer::text)`, account); err != nil {
			t.Fatal(err)
		}
	}
	newSystem := func() *System { return NewSystem(p, nil, nil, nil, nil, nil, nil, testPushCipher(t), nil, nil) }
	s := newSystem()
	in := IncomingNotification{Source: "integration", EventID: "event-1", Title: "A reminder", Message: "Open your collection", URL: "/notifications"}
	type outcome struct {
		result IncomingResult
		err    error
	}
	outcomes := make(chan outcome, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() { result, err := s.ReceiveIncoming(ctx, 1, "default", in); outcomes <- outcome{result, err} })
	}
	wg.Wait()
	close(outcomes)
	var id string
	accepted := 0
	for out := range outcomes {
		if out.err != nil {
			t.Fatal(out.err)
		}
		if out.result.Status == "accepted" {
			accepted++
		} else if out.result.Status != "duplicate" {
			t.Fatal(out.result)
		}
		if id != "" && id != out.result.DeliveryID {
			t.Fatal("concurrent replay created another identity")
		}
		id = out.result.DeliveryID
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d, want one", accepted)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM push_delivery_attempts WHERE notification_delivery_id=$1 AND push_device_id='device-1'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM push_delivery_attempts WHERE push_device_id='device-2'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("foreign profile received push", count, err)
	}
	if _, err := s.Deliveries.MarkRead(ctx, 1, "default", id); err != nil {
		t.Fatal(err)
	}
	s = newSystem()
	replay, err := s.ReceiveIncoming(ctx, 1, "default", in)
	if err != nil || replay.Status != "duplicate" || replay.DeliveryID != id {
		t.Fatal(replay, err)
	}
	row, err := s.Deliveries.GetRowByID(ctx, id)
	if err != nil || row == nil || row.ReadAt == nil {
		t.Fatal("restart/replay changed read state", err)
	}
	display := BuildNotificationDisplay(*row)
	if display.Title != "A reminder" || display.Body != "Open your collection" || display.URL != "/notifications" {
		t.Fatal(display)
	}
	changed := in
	changed.Title = "Different content"
	if _, err := s.ReceiveIncoming(ctx, 1, "default", changed); !errors.Is(err, ErrIncomingConflict) {
		t.Fatal(err)
	}
	other, err := s.ReceiveIncoming(ctx, 2, "default", in)
	if err != nil || other.Status != "accepted" || other.DeliveryID == id {
		t.Fatal(other, err)
	}
	checkIncomingSuppressionAndRollback(t, p, s, in)
}

func checkIncomingSuppressionAndRollback(t *testing.T, p *pgxpool.Pool, s *System, in IncomingNotification) {
	t.Helper()
	ctx := t.Context()
	var count int
	// The same event identity on a sibling profile is independent too.
	sibling, err := s.ReceiveIncoming(ctx, 1, "sibling", in)
	if err != nil || sibling.Status != "accepted" || sibling.DeliveryID == "" {
		t.Fatal(sibling, err)
	}
	prefs := DefaultPreferences(1, "quiet")
	prefs.Enabled = false
	if err := s.Preferences.Upsert(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	quiet, err := s.ReceiveIncoming(ctx, 1, "quiet", in)
	if err != nil || quiet.Status != "disabled" || quiet.DeliveryID != "" {
		t.Fatal(quiet, err)
	}
	expired := in
	expired.EventID = "expired"
	stamp := time.Now().Add(-time.Minute).UnixMilli()
	expired.ExpiresAt = &stamp
	late, err := s.ReceiveIncoming(ctx, 1, "default", expired)
	if err != nil || late.Status != "expired" || late.DeliveryID != "" {
		t.Fatal(late, err)
	}
	// A notification accepted before its deadline must stop at the sender
	// after its deadline too, including after queue recovery.
	queued := in
	queued.EventID = "queued-expiry"
	future := time.Now().Add(time.Hour).UnixMilli()
	queued.ExpiresAt = &future
	queuedResult, err := s.ReceiveIncoming(ctx, 1, "default", queued)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE notification_deliveries SET reason_flags=jsonb_set(reason_flags,'{expires_at}',to_jsonb($2::bigint)) WHERE id=$1`, queuedResult.DeliveryID, stamp); err != nil {
		t.Fatal(err)
	}
	var attempt PushDeliveryAttempt
	attempt.NotificationDeliveryID = &queuedResult.DeliveryID
	attempt.PushDeviceID = "device-1"
	if err := p.QueryRow(ctx, `SELECT id FROM push_delivery_attempts WHERE notification_delivery_id=$1`, queuedResult.DeliveryID).Scan(&attempt.ID); err != nil {
		t.Fatal(err)
	}
	final := s.pushSender.processAttempt(ctx, attempt)
	if final == nil || final.Outcome != PushOutcomeFailed || final.UpstreamReason == nil || *final.UpstreamReason != "delivery_expired" {
		t.Fatal("expired alert reached sending", final)
	}
	// A failed outbox enqueue must roll back the inbox row as well.
	if _, err := p.Exec(ctx, `ALTER TABLE push_delivery_attempts ADD CONSTRAINT reject_new_attempt CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	failed := in
	failed.EventID = "failed"
	if _, err := s.ReceiveIncoming(ctx, 1, "default", failed); err == nil {
		t.Fatal("expected enqueue failure")
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE reason_flags->>'event_id'='failed'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed enqueue left an inbox row", count, err)
	}
}

func TestIncomingNotificationRejectsUnsafeContent(t *testing.T) {
	base := IncomingNotification{Source: "integration", EventID: "one", Title: "Reminder"}
	for _, destination := range []string{"https://example.test/", "//example.test/", "/\\example.test/", "/%2f%2fexample.test/", "/%5cexample.test/", "javascript:alert(1)", "/%0a/"} {
		t.Run(destination, func(t *testing.T) {
			in := base
			in.URL = destination
			if !errors.Is(in.validate(), ErrIncomingInvalid) {
				t.Fatal("accepted unsafe destination")
			}
		})
	}
	for _, mutate := range []func(*IncomingNotification){
		func(in *IncomingNotification) { in.Title = " \t" },
		func(in *IncomingNotification) { in.Source = "other/source" },
		func(in *IncomingNotification) { in.EventID = "\n" },
		func(in *IncomingNotification) { in.Message = strings.Repeat("x", 4001) },
	} {
		in := base
		mutate(&in)
		if !errors.Is(in.validate(), ErrIncomingInvalid) {
			t.Fatal("accepted malformed event")
		}
	}
	for _, destination := range []string{"", "/notifications", "/item/example", "/api/v2/plugin-content/plugins/1/app#/details"} {
		in := base
		in.URL = destination
		if err := in.validate(); err != nil {
			t.Fatal(destination, err)
		}
	}
	stamp := time.Now().Add(-time.Minute).UnixMilli()
	in := base
	in.ExpiresAt = &stamp
	raw, _ := json.Marshal(in)
	row := DeliveryRow{Delivery: Delivery{Type: DeliveryTypeIncoming, ReasonFlags: raw}}
	if !deliveryExpired(row, time.Now()) {
		t.Fatal("expired incoming notification remains deliverable")
	}
	row.Type = DeliveryTypeEpisodeAvailable
	if deliveryExpired(row, time.Now()) {
		t.Fatal("incoming expiry affected another notification type")
	}
}
