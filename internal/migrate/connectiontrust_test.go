// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// authority is a certificate authority made for one test, and the server certificates it signs.
type authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         []byte
}

func newAuthority(t *testing.T, name string) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return authority{certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// serverCertificate is a certificate for the database's address, signed by this authority.
func (a authority) serverCertificate(t *testing.T, names ...string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: names,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsInFrontOf serves TLS in front of the test database and returns its port and the certificate
// it presents, which a test replaces.
//
// It is the database with a certificate, not a stand-in for one. A client asks for TLS the way
// PostgreSQL clients do, the handshake is a real one against the certificate held here, and
// everything after it is passed through to the real server untouched — so a connection that is
// accepted goes on to log in and run statements, and one that is refused is refused by the
// driver's own verification and nothing else.
func tlsInFrontOf(t *testing.T, database string) (port string, presented *atomic.Pointer[tls.Certificate]) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	presented = &atomic.Pointer[tls.Certificate]{}
	settings := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return presented.Load(), nil }}
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				// The request for TLS: a length of eight and the protocol's code for it. Answered
				// with the one byte that says yes.
				var request [8]byte
				if _, err := io.ReadFull(client, request[:]); err != nil ||
					binary.BigEndian.Uint32(request[:4]) != 8 || binary.BigEndian.Uint32(request[4:]) != 80877103 {
					return
				}
				if _, err := client.Write([]byte{'S'}); err != nil {
					return
				}
				secured := tls.Server(client, settings)
				if err := secured.Handshake(); err != nil {
					return
				}
				upstream, err := net.Dial("tcp", database)
				if err != nil {
					return
				}
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, secured); upstream.Close() }()
				_, _ = io.Copy(secured, upstream)
			}()
		}
	}()
	_, port, err = net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port, presented
}

// TestANewConnectionIsVerifiedAgainstTheAuthorityAsItIsNow holds a long-running process to the
// certificate authority its deployment currently publishes.
//
// An authority issued inside a cluster is replaced on a schedule measured in weeks. A pool that
// read the authority's file once would verify against the old one until it expired, and then
// refuse every new connection until something restarted the process. Here the authority is
// replaced under a pool that is already running, and the pool has to go on connecting — verified
// against the replacement, which is the half that makes it verification and not its absence: the
// old authority's certificate, presented after the replacement, is refused.
func TestANewConnectionIsVerifiedAgainstTheAuthorityAsItIsNow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// A schema and a login the runtime pool accepts, so the pool under test is the one a serving
	// process builds, with the privilege check it runs on every connection it opens.
	owner := testPool(t)
	schema, _ := NewSchema("trust_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	establish(t, owner, schema.String())
	role := "trust_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := ensureRole(ctx, owner, role, "isolated-trust-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE")
		owner.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		owner.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize())
	})
	if _, err := owner.Exec(ctx, "GRANT "+pgx.Identifier{DataRole}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	database, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}

	first, second := newAuthority(t, "the authority at start"), newAuthority(t, "its replacement")
	port, presented := tlsInFrontOf(t, database.Host)
	presented.Store(first.serverCertificate(t, "localhost"))
	published := filepath.Join(t.TempDir(), "ca.crt")
	publish := func(contents []byte) {
		t.Helper()
		// Replaced the way a mounted secret is: written beside and renamed over, so a reader
		// sees the old file or the new one and never half of either.
		next := published + ".next"
		if err := os.WriteFile(next, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, published); err != nil {
			t.Fatal(err)
		}
	}
	publish(first.pem)

	verified := *database
	verified.User = url.UserPassword(role, "isolated-trust-test")
	verified.Host = net.JoinHostPort("localhost", port)
	query := verified.Query()
	query.Set("sslmode", "verify-full")
	query.Set("sslrootcert", published)
	verified.RawQuery = query.Encode()
	dsn := verified.String()

	following := func() *pgxpool.Pool {
		t.Helper()
		pool, err := NewRuntimePool(ctx, dsn, schema, MemoryPlane)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	// newConnection makes the pool open a physical connection and run a statement on it. Reset
	// closes what the pool holds, so the next statement cannot be served by a connection that was
	// verified earlier.
	newConnection := func(pool *pgxpool.Pool) error {
		pool.Reset()
		var one int
		return pool.QueryRow(ctx, `SELECT 1`).Scan(&one)
	}
	refusedForItsCertificate := func(err error) bool {
		return err != nil && (strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate"))
	}

	pool := following()
	// The same string in a pool that reads the file once, as every pool did. It is the control:
	// what happens to it below is the defect.
	readOnce, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnce.Close)

	if err := newConnection(pool); err != nil {
		t.Fatalf("a connection verified against the authority that signed the server's certificate was refused: %v", err)
	}
	if err := newConnection(readOnce); err != nil {
		t.Fatalf("the control pool could not connect before anything changed: %v", err)
	}
	// A real session behind the handshake: it logged in as the role and is talking to the database.
	var who string
	if err := pool.QueryRow(ctx, `SELECT session_user`).Scan(&who); err != nil || who != role {
		t.Fatalf("the verified connection is not a session of %s on the database: %q, %v", role, who, err)
	}
	// The pool the management surface and the commands hold follows the authority too.
	administrative, err := NewAdminPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(administrative.Close)
	if err := newConnection(administrative); err != nil {
		t.Fatalf("the administrative pool could not connect before anything changed: %v", err)
	}

	// The authority is replaced, and the server presents a certificate from the new one.
	presented.Store(second.serverCertificate(t, "localhost"))
	publish(second.pem)

	if err := newConnection(pool); err != nil {
		t.Fatalf("after the authority was replaced, a process that was already running can no longer connect: %v", err)
	}
	if err := newConnection(administrative); err != nil {
		t.Fatalf("after the authority was replaced, the administrative pool can no longer connect: %v", err)
	}
	if err := newConnection(readOnce); !refusedForItsCertificate(err) {
		t.Fatalf("the control pool, which read the authority once, was expected to refuse the replacement's certificate; got %v", err)
	}

	// It is verification, not its absence: the old authority's certificate is no longer accepted.
	presented.Store(first.serverCertificate(t, "localhost"))
	if err := newConnection(pool); !refusedForItsCertificate(err) {
		t.Fatalf("a certificate from the authority that was replaced was accepted: %v", err)
	}
	// And neither is a certificate from the right authority for another name.
	presented.Store(second.serverCertificate(t, "not-the-database"))
	if err := newConnection(pool); !refusedForItsCertificate(err) {
		t.Fatalf("a certificate for another host name was accepted: %v", err)
	}
	presented.Store(second.serverCertificate(t, "localhost"))
	if err := newConnection(pool); err != nil {
		t.Fatalf("the pool did not recover once the right certificate was presented again: %v", err)
	}

	// Trust material that has gone missing fails the connection. It does not fall back to the
	// authority read earlier, and it does not connect unverified.
	if err := os.Remove(published); err != nil {
		t.Fatal(err)
	}
	if err := newConnection(pool); err == nil {
		t.Fatal("with the authority's file gone, a new connection was opened anyway")
	}
	publish([]byte("not a certificate"))
	if err := newConnection(pool); err == nil {
		t.Fatal("with the authority's file unreadable as a certificate, a new connection was opened anyway")
	}
	publish(second.pem)
	if err := newConnection(pool); err != nil {
		t.Fatalf("the pool did not recover once the authority's file was back: %v", err)
	}
}
