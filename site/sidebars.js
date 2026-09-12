// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The navigation is written once, in docs/SUMMARY.md, where it also reads as a table of contents on
// GitHub. internal/docsite turns it into this file's input: a `guide` sidebar for the written
// documents and a `reference` sidebar for the generated reference, which is several hundred pages
// and would bury everything else if the two shared one.
module.exports = require('./sidebars.generated.json');
