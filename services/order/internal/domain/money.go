package domain

import "strings"

// currencyCodeLen is the length of an ISO 4217 alphabetic code.
const currencyCodeLen = 3

// CurrencyCode is an ISO 4217 alphabetic code, uppercased.
//
// Which codes this system actually sells in is not decided here: a list in the
// domain would make adding a currency a deploy of this service, and the answer
// belongs to whoever runs the shop. What is checked is the shape the schema
// relies on, so CHECK (total_currency ~ '^[A-Z]{3}$') holds for every row this
// package can produce.
type CurrencyCode string

// NewCurrencyCode normalises and checks a code.
func NewCurrencyCode(s string) (CurrencyCode, error) {
	invalid := ValidationError{Field: "currency_code", Message: "is not a three-letter ISO 4217 code"}

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

// Money is an amount in one currency, and in this service it is always money
// somebody owes.
//
// The amount is in the currency's minor unit — 1050 is THB 10.50 — so no amount
// here is ever a binary float, and the number of decimal places is a property of
// the code rather than of the field.
//
// It is copied from catalog rather than shared with it, which is the trade this
// repo makes everywhere a domain concept appears twice: a shared Money would be
// one type two services could not change independently, and the day one of them
// needs a rounding rule the other must not have, the shared version is the
// problem rather than the saving.
type Money struct {
	amountMinor int64
	currency    CurrencyCode
}

// NewMoney builds an amount.
//
// A negative amount is refused although ecommerce.common.v1.Money declares no
// such bound: that message is also the shape of a discount and a refund, and a
// constraint on it would forbid those everywhere it is reused. Nothing an order
// holds is negative, so this is where it is rejected.
func NewMoney(amountMinor int64, currency CurrencyCode) (Money, error) {
	if currency == "" {
		return Money{}, ValidationError{Field: "currency_code", Message: "is required"}
	}

	if amountMinor < 0 {
		return Money{}, ValidationError{Field: "amount_minor", Message: "is negative"}
	}

	return Money{amountMinor: amountMinor, currency: currency}, nil
}

// ReconstituteMoney rebuilds an amount from storage.
//
// It validates nothing, for the reason every Reconstitute here does not: a rule
// tightened afterwards must not make existing rows unreadable, and a repository
// that had to call NewMoney would fail to load the very orders it needs to be
// able to correct.
func ReconstituteMoney(amountMinor int64, currency CurrencyCode) Money {
	return Money{amountMinor: amountMinor, currency: currency}
}

// AmountMinor returns the amount in the currency's minor unit.
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency returns the code the amount is denominated in.
func (m Money) Currency() CurrencyCode { return m.currency }

// IsZero reports an unset amount. A zero Money is "unpriced", not "free": the
// pair is the value, and an amount without a currency is not money.
func (m Money) IsZero() bool { return m.amountMinor == 0 && m.currency == "" }
