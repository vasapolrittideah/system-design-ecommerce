package rest

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
)

// The shapes this API speaks, and the mapping between them and the protos behind
// it. Every field a client sees is camelCase.
//
// They are hand-written rather than generated from the protos, so a field a
// service adds to its contract does not appear on the public API by accident,
// and a client is never bound to a shape that exists for east-west traffic. That
// separation is also why the `validate` tags restate rules the protos already
// declare: a BFF is the one place in the repo protovalidate cannot reach, and an
// unchecked request would otherwise cost a network round trip to be rejected.
//
// One file per screen — auth_dto.go, products_dto.go — named after the handler
// file it serves. This one holds what more than one of them needs, which is the
// only reason for a shape to live here rather than beside its screen.

// userResponse is a person as every screen that shows one returns them:
// registration, login, and /me all answer with this rather than three views of
// the same account.
type userResponse struct {
	User user `json:"user"`
}

type user struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// toUser maps the service's view of a person onto the public one.
//
// Roles come back as an empty array rather than null for a user with none, so a
// client can iterate without a nil check — JSON's two ways of saying "nothing"
// are one more branch than any caller needs.
func toUser(u *identityv1.User) user {
	roles := u.GetRoles()
	if roles == nil {
		roles = []string{}
	}

	return user{
		ID:        u.GetId(),
		Email:     u.GetEmail(),
		Roles:     roles,
		CreatedAt: asTime(u.GetCreatedAt()),
		UpdatedAt: asTime(u.GetUpdatedAt()),
	}
}

// asTime converts a proto timestamp, mapping an absent one to the zero time
// rather than to 1970 — AsTime on a nil timestamp answers the Unix epoch, which
// would reach a client as a real date it could render.
func asTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}

	return ts.AsTime()
}
