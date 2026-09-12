package pg

// FactsAboutQueryForTesting is the current read's traversal, so the plan test runs the statement
// recall runs rather than a copy of it.
var FactsAboutQueryForTesting = selectFactsAboutSQL
