package app

import "time"

// Policy is what this service decides rather than the domain or a caller: how
// long a hold survives, and where the current time comes from.
//
// The TTL is here and not on ReserveStockCommand because a caller allowed to
// choose it would choose forever, and the point of an expiry is that the
// warehouse gets its capacity back from an order nobody finished. It is here and
// not in the domain because it is a deployment's number, arriving through
// pkg/config like every other one.
type Policy struct {
	// ReservationTTL is how long a new hold lasts. It has to exceed the longest
	// a checkout can legitimately take — a payment provider redirect is minutes,
	// not seconds — because a hold that expires under a shopper who is still
	// paying turns into a refund.
	ReservationTTL time.Duration

	// Now is the clock. A field rather than a call to time.Now, so that the
	// expiry rules can be driven from a test without one sleeping through a TTL.
	Now func() time.Time
}
