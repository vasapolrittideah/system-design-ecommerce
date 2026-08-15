-- Refresh tokens are the half of the credential pair that can be taken back.
-- An access token is a signed JWT that nothing can revoke before it expires,
-- so the ability to end a session lives here, in rows that can be updated and
-- deleted.

-- +goose Up

CREATE TABLE refresh_tokens (
    id         uuid        PRIMARY KEY,

    -- ON DELETE CASCADE because a deleted user has no sessions. This is a
    -- foreign key rather than an application-level check because both tables
    -- are this service's own — the rule about never reaching into another
    -- service's data is about other services, not about giving up referential
    -- integrity inside one database.
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- SHA-256 of the token, never the token. A stolen database dump then
    -- contains nothing that can be presented to this service.
    --
    -- Deliberately a fast hash, where passwords use argon2id: the input here
    -- is 256 bits of output from a CSPRNG, so there is no dictionary to run
    -- and nothing for a slow hash to buy. It also has to be deterministic —
    -- the lookup is by exact value, which a per-row salt would make
    -- impossible.
    --
    -- UNIQUE does double duty: it is the index every refresh reads through,
    -- and it makes a repeated hash a write that fails rather than a second row
    -- that silently shadows the first.
    token_hash bytea       NOT NULL UNIQUE,

    -- The rotation chain this token belongs to. Every refresh spends one token
    -- and issues another with the same family_id, so presenting a token that
    -- was already spent proves a copy exists — and the answer is to revoke the
    -- whole family, ending the session for the thief and the owner alike. The
    -- owner can sign in again; the thief cannot.
    family_id  uuid        NOT NULL,

    -- The chain's fixed end. Rotation does not extend it, or a token that is
    -- refreshed often enough would outlive every policy that was meant to
    -- bound it.
    expires_at timestamptz NOT NULL,

    -- NULL while the token can still be presented. It is set when the token is
    -- spent by a rotation, and when a logout or a reuse revokes the family.
    --
    -- Spending a token is `UPDATE ... WHERE id = $1 AND revoked_at IS NULL`,
    -- and the row count is the answer: two concurrent refreshes holding the
    -- same token must not both succeed, and letting the database decide is the
    -- only way that does not have a race between the read and the write.
    revoked_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1
);

-- Revoking a family reads by family_id, which happens on every logout and on
-- every detected reuse.
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);

-- Reading a user's sessions: what a cascade delete walks, and what an
-- administrative "sign this account out everywhere" would use.
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);

-- There is deliberately no index on expires_at. The only reader is a periodic
-- sweep deleting rows that are long dead, which runs against a table bounded
-- by the number of live sessions rather than by history — a sequential scan
-- there costs less than an index kept current on every write.

-- +goose Down

DROP TABLE refresh_tokens;
