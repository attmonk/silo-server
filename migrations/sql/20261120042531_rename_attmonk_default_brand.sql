-- +goose Up
-- Only migrate the bundled defaults; keep administrator-defined names.
UPDATE server_settings
SET value = 'ATTMonk Server'
WHERE key = 'branding.server_name' AND value IN ('Silo', 'ATTMonk Server');

-- +goose Down
-- A saved display name is a user preference; retain it on an image rollback.
SELECT 1;
