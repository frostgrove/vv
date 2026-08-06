CREATE TABLE users (
	id         BIGSERIAL PRIMARY KEY,
	tenant_id  BIGINT       NOT NULL,
	email      VARCHAR(255) NOT NULL,
	name       VARCHAR(255) NOT NULL,
	age        INTEGER          NULL,
	active     BOOLEAN      NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
