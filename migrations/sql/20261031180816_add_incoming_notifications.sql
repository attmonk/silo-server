-- +goose Up
CREATE UNIQUE INDEX notification_deliveries_incoming_event_key
ON notification_deliveries(user_id, profile_id, (reason_flags->>'source'), (reason_flags->>'event_id'))
WHERE type = 'external.notification';

-- +goose Down
DROP INDEX notification_deliveries_incoming_event_key;
