-- What rotation adds to the sessions table.
--
-- Copy this beside the access migration and rename it with your own timestamp.
-- It is separate because a deployment that holds opaque sessions never rotates
-- and has no use for either column.
--
-- The session row *is* the family: one sign-in, one lineage of refresh
-- credentials. Nothing here holds a second id for it, because a family that
-- could outlive its session would be a second thing to close on sign-out and a
-- second thing to forget.

-- +goose Up

-- The digest of the refresh credential this session accepted before the last
-- rotation. Presenting it inside the grace window is two tabs refreshing at
-- once; presenting it after is a replay, and the session is closed.
ALTER TABLE "sessions" ADD COLUMN IF NOT EXISTS "previous_token_hash" TEXT NOT NULL DEFAULT '';
ALTER TABLE "sessions" ADD COLUMN IF NOT EXISTS "rotated_at" TIMESTAMPTZ;

-- A rotation looks the previous digest up as readily as the current one.
CREATE INDEX IF NOT EXISTS "ix_sessions_previous_token_hash"
    ON "sessions" ("previous_token_hash") WHERE "previous_token_hash" <> '';

-- +goose Down
DROP INDEX IF EXISTS "ix_sessions_previous_token_hash";
ALTER TABLE "sessions" DROP COLUMN IF EXISTS "rotated_at";
ALTER TABLE "sessions" DROP COLUMN IF EXISTS "previous_token_hash";
