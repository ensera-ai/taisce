// Taisce — memory for agents.
//
// Go 1.27.3, pinned exactly. The `go` directive is the oldest release the source needs; the
// `toolchain` line is the release that builds it, and it is what makes a build on any other version
// fetch this one instead of proceeding. A `go` directive alone is a minimum, so without the second
// line a machine holding something newer builds with that and reports success — which is how a local
// run and a hosted run came to measure the same tree with two different compilers (#171).
//
// The version is 1.27.3 and not 1.27.1 because this code reaches twelve advisories in the 1.27.1
// standard library — `html/template` through the operator portal's renderer, `http2` through the
// server's own Serve, through `writeJSON` and through the notification sender. Measured with
// govulncheck on 2026-11-09: thirteen reachable here, zero under 1.27.3 with #163 in (#162).
module github.com/ensera-ai/taisce

go 1.27.1

toolchain go1.27.3


require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
	golang.org/x/text v0.43.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)
