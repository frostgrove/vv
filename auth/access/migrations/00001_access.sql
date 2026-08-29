-- The access context's seven tables.
--
-- Copy this file into your own migrations directory and rename it with your own
-- timestamp. It is a file to copy and not an embedded FS on purpose: a
-- migration is a fact about *your* schema, applied on your schedule and edited
-- when your deployment needs a column this module never asked for. A library
-- that owned it would own when your database changes.
--
-- Nothing here references an accounts table. That is the whole design: a role,
-- a credential and a session point at a *subject* — a type and an id — the way
-- Laravel's model_has_roles points at a morph, so a second kind of caller needs
-- an implementation of access.Directory and no migration here.
--
-- The price of that is stated rather than hidden: subject_type/subject_id
-- cannot be a foreign key, so nothing at the database level deletes a subject's
-- grants when the subject goes. Deactivate rather than delete — a grant
-- pointing at a deactivated subject grants nothing, because the directory is
-- asked whether it is active on every request.
--
-- Every id is uuid with a database default, so a client can never choose one:
-- vv marks a `pk,auto` column and clears whatever a request body put there.
--
-- Adding columns of your own to these tables is safe. A repository selects the
-- columns its model names, so a column this module has never heard of is read
-- and written by your code alone.

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS "permissions" (
    "id"         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "code"       TEXT        NOT NULL,
    "name"       TEXT        NOT NULL DEFAULT '',
    -- Which bounded context declared it. An orphaned code from a module that no
    -- longer exists is then visible rather than merely inert.
    "module"     TEXT        NOT NULL DEFAULT '',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_permissions_code" ON "permissions" ("code");

CREATE TABLE IF NOT EXISTS "roles" (
    "id"         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "slug"       TEXT        NOT NULL,
    "name"       TEXT        NOT NULL DEFAULT '',
    -- A system role is seeded by the application and refused to renames and
    -- deletes: a deployment that lost "admin" has no way back in.
    "is_system"  BOOLEAN     NOT NULL DEFAULT FALSE,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_roles_slug" ON "roles" ("slug");

CREATE TABLE IF NOT EXISTS "role_permissions" (
    "id"            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "role_id"       UUID NOT NULL REFERENCES "roles" ("id") ON DELETE CASCADE,
    "permission_id" UUID NOT NULL REFERENCES "permissions" ("id") ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_role_permissions" ON "role_permissions" ("role_id", "permission_id");
CREATE INDEX IF NOT EXISTS "ix_role_permissions_permission" ON "role_permissions" ("permission_id");

CREATE TABLE IF NOT EXISTS "subject_roles" (
    "id"           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "subject_type" TEXT        NOT NULL,
    "subject_id"   UUID        NOT NULL,
    "role_id"      UUID        NOT NULL REFERENCES "roles" ("id") ON DELETE CASCADE,
    "granted_at"   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_subject_roles"
    ON "subject_roles" ("subject_type", "subject_id", "role_id");

CREATE TABLE IF NOT EXISTS "subject_permissions" (
    "id"            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "subject_type"  TEXT        NOT NULL,
    "subject_id"    UUID        NOT NULL,
    "permission_id" UUID        NOT NULL REFERENCES "permissions" ("id") ON DELETE CASCADE,
    "granted_at"    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_subject_permissions"
    ON "subject_permissions" ("subject_type", "subject_id", "permission_id");

CREATE TABLE IF NOT EXISTS "credentials" (
    "id"           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "subject_type" TEXT        NOT NULL,
    "subject_id"   UUID        NOT NULL,
    "provider"     TEXT        NOT NULL,
    -- Stored exactly as the application supplied it. Normalising an identifier
    -- is the application's job, so this index is over whatever rule it applied.
    "identifier"   TEXT        NOT NULL,
    "secret_hash"  TEXT        NOT NULL,
    "created_at"   TIMESTAMPTZ NOT NULL DEFAULT now(),
    "updated_at"   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Unique within a subject type and not across all of them: two independent
-- domains may both know an "ops@example.com", and making one of them rename to
-- register is a rule the database has no business inventing. The consequence is
-- that a sign-in has to say which type it is for — see LoginCommand.Subject —
-- because (provider, identifier) alone is no longer a question with one answer.
CREATE UNIQUE INDEX IF NOT EXISTS "uq_credentials_subject_type_provider_identifier"
    ON "credentials" ("subject_type", "provider", "identifier");
CREATE INDEX IF NOT EXISTS "ix_credentials_subject"
    ON "credentials" ("subject_type", "subject_id");

CREATE TRIGGER "credentials_set_updated_at"
    BEFORE UPDATE ON "credentials"
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS "sessions" (
    "id"             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "subject_type"   TEXT        NOT NULL,
    "subject_id"     UUID        NOT NULL,
    -- The digest, never the token. Reading this table must not be enough to
    -- impersonate anybody, which is what makes a backup, a slow-query log and a
    -- support engineer's SELECT all harmless.
    "token_hash"     TEXT        NOT NULL,
    "user_agent"     TEXT        NOT NULL DEFAULT '',
    "ip"             TEXT        NOT NULL DEFAULT '',
    "created_at"     TIMESTAMPTZ NOT NULL DEFAULT now(),
    "last_used_at"   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Absolute. No refresh moves it, which is what bounds a session's total
    -- life however active the caller is.
    "expires_at"     TIMESTAMPTZ NOT NULL,
    "revoked_at"     TIMESTAMPTZ,
    "revoked_reason" TEXT        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_sessions_token_hash" ON "sessions" ("token_hash");
-- The authenticated-request path is a lookup by digest; the session list and
-- logout-all are a lookup by subject. Those are the only two reads there are.
CREATE INDEX IF NOT EXISTS "ix_sessions_subject"
    ON "sessions" ("subject_type", "subject_id", "revoked_at");
CREATE INDEX IF NOT EXISTS "ix_sessions_expires_at" ON "sessions" ("expires_at");

-- +goose Down
DROP TABLE IF EXISTS "sessions";
DROP TABLE IF EXISTS "credentials";
DROP TABLE IF EXISTS "subject_permissions";
DROP TABLE IF EXISTS "subject_roles";
DROP TABLE IF EXISTS "role_permissions";
DROP TABLE IF EXISTS "roles";
DROP TABLE IF EXISTS "permissions";
