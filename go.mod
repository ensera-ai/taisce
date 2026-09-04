// Taisce — memory for agents.
//
// Go 1.27.0, pinned exactly: a `go` directive plus a `toolchain` line means the version this module
// is built with is a decision recorded in the file rather than whatever the builder happens to have.
module github.com/taisce/taisce

go 1.27.1

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
)
