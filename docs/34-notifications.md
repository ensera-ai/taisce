<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Being told that memory formed

When you write a turn, Taisce stores it at once, but forming facts from it happens a little later.
`GET /v1/freshness` tells you whether formation has caught up, but you have to keep asking, and most
of those polls only learn that nothing changed.

Notifications turn that around. Register an HTTPS endpoint, and Taisce sends it a small signed
message each time formation moves forward in your project.

## Turn it on (operator)

Notifications are off until an operator names the hosts a project may send to. Every other outbound
call Taisce makes goes to an address an operator configured. A notification goes to an address a
client supplies through the API, so the operator decides which hosts are allowed:

```sh
TAISCE_NOTIFY_DESTINATIONS=hooks.example.com,.partner.example
```

Each entry is an exact hostname, or starts with a dot to allow its subdomains. With the variable
unset, all four routes below answer `501 notifications_unavailable`. They refuse rather than accept
and ignore, because an application that registered an endpoint and got a receipt would reasonably
expect to be told.

A destination also must not resolve to an address only this deployment can reach: loopback, private
ranges, link-local, carrier-grade NAT or IPv6 unique-local. That check runs when the endpoint is
registered **and again before every delivery**, because a DNS answer can change after it was
approved. If your receiver really is on the same private network, say so:

```sh
TAISCE_NOTIFY_PERMIT_PRIVATE=true
```

Destinations must use HTTPS and must not carry credentials in the URL. The signature is the
authentication.

A few more rules hold every time a delivery is sent:

- **Redirects are not followed.** If your endpoint answers with a `3xx`, the delivery ends there and
  is recorded as `redirect not followed`. A webhook has no reason to send the signed body somewhere
  else, so Taisce doesn't follow it.
- **The address is checked where Taisce connects.** A name could resolve to a public address when it
  is checked and to a private one a moment later, when the connection is made. So the same rule runs
  again on the address actually being connected to. `TAISCE_NOTIFY_PERMIT_PRIVATE=true` turns this
  check off too.
- **No proxy.** Notifications ignore `HTTPS_PROXY` and similar settings, because through a proxy
  neither check would see the real destination. A deployment that can only reach the internet
  through a proxy can't send notifications today.

## Register an endpoint

| Operation | Route | Credential |
|---|---|---|
| Register | `POST /v1/notifications/endpoints/register` | read-write |
| List | `POST /v1/notifications/endpoints/list` | read-only is enough |
| Disable | `POST /v1/notifications/endpoints/disable` | read-write |
| List deliveries | `POST /v1/notifications/deliveries/list` | read-only is enough |

```http
POST /v1/notifications/endpoints/register
Authorization: Bearer <project-write-credential>
Content-Type: application/json

{"url":"https://hooks.example.com/taisce"}
```

The response includes the endpoint's `id`, `url`, `created_at` and a `secret`. **You see the secret
once.** Taisce keeps it to sign deliveries but never shows it again. If you lose it, register again
and disable the old endpoint with `{"id":"<endpoint-id>"}`.

Registering an endpoint is a write, and it is recorded in the audit log, because it decides where this
deployment sends outbound requests.

## What a delivery looks like

```json
{"version":"taisce-notification/v1","scope":"p1","formed_through":412,
 "stored_through":418,"parked_turns":0,"occurred_at":"2026-09-11T09:44:20Z"}
```

A project, two log offsets and a count. **No subject and no content**, on purpose. You read the
content back through the authenticated API, so a notification sent to the wrong place only reveals
that a project formed something, not what. Delivery records do not name a data subject either. If
they did, every delivery would be personal data that an erasure would have to cover.

Two headers come with it:

```http
Taisce-Signature: v1 t=1789128260 s=<hex hmac-sha256>
Taisce-Delivery: <uuid>
```

## Check the signature

The signature is an HMAC-SHA256, with your secret, over the version, the timestamp and the body,
joined by newlines:

```text
material = "v1" + "\n" + t + "\n" + body
expected = "v1 t=" + t + " s=" + hex(hmac_sha256(secret, material))
```

Compare in constant time, and reject a timestamp outside a tolerance you choose. Without that
check the timestamp does nothing. Signing the timestamp is what makes a replay detectable: a
signature over the body alone stays valid forever, so anyone who captured one delivery could resend
it a week later.

`Verify` in [`internal/notify/signature.go`](../internal/notify/signature.go) is the reference
implementation.

## Retries and parking

Formation never waits for a delivery. A delivery is saved as a row first, and sent afterwards.
Sending inside the formation transaction would either roll back a turn because someone's endpoint
was down, or hide the failure.

Delivery is **at least once**. Log offsets increase steadily within a project, so a receiver that
remembers the highest offset it has handled can drop a repeat without coordinating with anyone.
Exactly-once delivery is not offered.

When an attempt fails:

- a `429` or a `5xx` answer is retried, waiting five seconds and doubling each time, for six attempts
  in all (about five minutes);
- a failed connection (a timeout, a refused connection, a TLS failure) is retried the same way;
- a redirect is final, as above;
- any other `4xx`, such as `400` or `404`, is final on the first attempt, because the receiver has
  said the request is wrong and sending it again unchanged will not help.

After the last attempt, the delivery is **parked**. That is a state you can ask about, not a log line
you have to find:

```http
POST /v1/notifications/deliveries/list
Authorization: Bearer <project-credential>
Content-Type: application/json

{"limit":50}
```

Each row shows the endpoint and URL, the offsets sent, how many attempts it took, the last status,
the last error, and whether it was delivered or parked. The last error is always one of a fixed list
of categories, never the raw network error: a raw error can name internal addresses, and any key in
the project can read this list.

| `last_error` | What happened | Retried |
|---|---|---|
| `destination not permitted` | not listed, not HTTPS, credentials in the URL, or resolves inside the deployment | no |
| `address not permitted` | the address actually connected to is inside the deployment | no |
| `redirect not followed` | the destination answered with a `3xx` | no |
| `destination answered NNN` | a `429` or a `5xx` | yes |
| `destination refused with NNN` | any other `4xx` | no |
| `timeout`, `connection refused`, `tls failure`, `network error` | the connection failed | yes |
| `unrecorded` | written before errors were recorded as categories; `last_status` still has the answer | no |

A database constraint keeps the column to this list, so a later change can't put an address back
into it by accident.

## What it does not do

- It does not tell you what formed, only that formation reached an offset. Read the content back.
- It does not guarantee order across projects.
- It does not retry a parked delivery. The receiver can catch up in one call using the watermark,
  which is why the payload is an offset.

## How it is tested

In `internal/api`, against a real deployment and a real HTTPS endpoint:
`TestAFormedScopeTellsItsEndpointAndTheNotificationCarriesNoMemory`,
`TestADestinationThatKeepsFailingIsRetriedThenParkedWhereAnOperatorCanSeeIt` and
`TestADeploymentWithNoPermittedDestinationRefusesEveryNotificationRoute`.

In `internal/notify`, each against a real listener:
`TestNothingIsSentUntilAnOperatorSaysWhereAndNeverIntoTheDeploymentsOwnNetwork`,
`TestASignatureFailsOnOneAlteredByteAndOnADeliveryReplayedLater`,
`TestARedirectFromAPermittedDestinationIsNeverFollowed`,
`TestARebindingAnswerIsRefusedOnTheAddressActuallyDialled`,
`TestAFailureIsStoredAsACategoryThatNamesNoAddress` and
`TestATransportTheSenderCannotGuardIsReplacedNotTrusted`.

In `internal/api`, against the database's constraint: `TestADeliveryFailureCanOnlyBeStoredAsACategory`.
