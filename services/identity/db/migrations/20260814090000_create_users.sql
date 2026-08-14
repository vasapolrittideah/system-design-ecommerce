-- +goose Up

CREATE TABLE users (
    -- No DEFAULT gen_random_uuid(). The aggregate mints its own id before it
    -- is persisted, because the domain event it raises carries that id and is
    -- written to the outbox in the same transaction as the row. A default here
    -- would never fire and would suggest the database decides identity.
    id            uuid        PRIMARY KEY,

    -- Stored already lowercased, so uniqueness is the plain constraint rather
    -- than a functional index or the citext extension. The CHECK is what makes
    -- a caller that forgets to normalise fail loudly instead of quietly
    -- creating a second account that looks identical to a human.
    email         text        NOT NULL UNIQUE CHECK (email = lower(email)),

    -- An argon2id encoded hash — the algorithm and its parameters travel
    -- inside the string, so the column survives a change of either. Never a
    -- fast hash: this is the one place where being slow is the feature.
    password_hash text        NOT NULL,

    -- An array rather than a join table. Roles are a small set owned entirely
    -- by this service, always read whole, and copied into the token at sign
    -- time; nothing here asks "who has role X". There is deliberately no CHECK
    -- listing the allowed values — which roles exist is a domain rule, and
    -- encoding it here would make adding one a migration.
    roles         text[]      NOT NULL DEFAULT '{}',

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    -- Optimistic locking. Every UPDATE carries the version it read and bumps
    -- it, so two concurrent writers cannot silently overwrite each other —
    -- the loser affects zero rows and finds out.
    version       int         NOT NULL DEFAULT 1
);

-- +goose Down

DROP TABLE users;
