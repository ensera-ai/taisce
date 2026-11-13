package pg

// FactsAboutQueryForTesting is the current read's traversal, so the plan test runs the statement
// recall runs rather than a copy of it.
var FactsAboutQueryForTesting = selectFactsAboutSQL

// The two reads of the past, for the same reason: what a row-security policy does to them is a
// property of these statements and no others.
var (
	FactsValidAtQueryForTesting = selectFactsValidAtSQL
	FactsKnownAtQueryForTesting = selectFactsKnownAtSQL
)
