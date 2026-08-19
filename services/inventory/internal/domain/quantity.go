package domain

// quantityMax bounds every count this service holds, matching what the proto
// declares. It is far above any real warehouse and exists so that an arithmetic
// mistake fails a validation rather than overflowing the int32 the column is.
const quantityMax = 1_000_000

// Quantity is a number of physical things: never negative, never absurd.
//
// int32 and not int, because that is the column and there is no point at which
// this service holds a count the database could not store.
type Quantity int32

// NewQuantity checks a count that arrived from outside.
//
// Zero is valid. A SKU that is tracked but not yet delivered is an ordinary
// state, and it is the one that keeps an order from being taken for it.
func NewQuantity(field string, n int32) (Quantity, error) {
	if n < 0 || n > quantityMax {
		return 0, ValidationError{Field: field, Message: "must be between 0 and 1000000"}
	}

	return Quantity(n), nil
}

// Int32 returns the count for the column and the wire.
func (q Quantity) Int32() int32 { return int32(q) }

// Adjustment is a signed change to a count — a delivery arriving, a breakage
// written off.
//
// A separate type from Quantity because the sign is the whole point: the two are
// not interchangeable, and a function that took one where it meant the other
// would compile.
type Adjustment int32

// NewAdjustment checks a change that arrived from outside.
//
// Zero is refused rather than accepted as a no-op. An adjustment of nothing is a
// caller that computed the wrong number, and answering "done" to it hides that
// for as long as it takes somebody to count a shelf.
func NewAdjustment(delta int32) (Adjustment, error) {
	switch {
	case delta == 0:
		return 0, ValidationError{Field: "delta", Message: "must not be zero"}
	case delta < -quantityMax || delta > quantityMax:
		return 0, ValidationError{Field: "delta", Message: "must be between -1000000 and 1000000"}
	}

	return Adjustment(delta), nil
}

// Int32 returns the change for the statement that applies it.
func (a Adjustment) Int32() int32 { return int32(a) }
