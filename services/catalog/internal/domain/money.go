package domain

import "strings"

// currencyCodeLen is the length of an ISO 4217 alphabetic code.
const currencyCodeLen = 3

// CurrencyCode is an ISO 4217 alphabetic code, uppercased.
//
// Which codes this system actually sells in is not decided here: a list in the
// domain would make adding a currency a deploy of this service, and the answer
// belongs to whoever runs the shop. What is checked is the shape the schema
// relies on, so CHECK (price_currency ~ '^[A-Z]{3}$') holds for every row this
// package can produce.
type CurrencyCode string

// NewCurrencyCode normalises and checks a code.
func NewCurrencyCode(s string) (CurrencyCode, error) {
	invalid := ValidationError{Field: "price.currency_code", Message: "is not a three-letter ISO 4217 code"}

	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != currencyCodeLen {
		return "", invalid
	}

	for i := range len(s) {
		if s[i] < 'A' || s[i] > 'Z' {
			return "", invalid
		}
	}

	return CurrencyCode(s), nil
}

// String returns the uppercased code.
func (c CurrencyCode) String() string { return string(c) }

// Money is an amount in one currency, and in this service it is always a price.
//
// The amount is in the currency's minor unit — 1050 is THB 10.50 — so no price
// here is ever a binary float, and the number of decimal places is a property of
// the code rather than of the field.
//
// Its fields are unexported because the pair is the invariant: an amount without
// its currency is not a price, and a zero Money is not "free" but "unpriced".
type Money struct {
	amountMinor int64
	currency    CurrencyCode
}

// NewMoney builds a price.
//
// A negative amount is refused here although ecommerce.common.v1.Money declares
// no such bound: that message is also the shape of a discount and a refund, and
// a constraint on it would forbid those everywhere it is reused. A negative
// price is only meaningless in this service, so this service is where it is
// rejected — and it is the same rule the migration's CHECK holds.
func NewMoney(amountMinor int64, currency CurrencyCode) (Money, error) {
	if currency == "" {
		return Money{}, ValidationError{Field: "price.currency_code", Message: "is required"}
	}

	if amountMinor < 0 {
		return Money{}, ValidationError{Field: "price.amount_minor", Message: "is negative"}
	}

	return Money{amountMinor: amountMinor, currency: currency}, nil
}

// AmountMinor returns the amount in the currency's minor unit.
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency returns the code the amount is denominated in.
func (m Money) Currency() CurrencyCode { return m.currency }
