<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Project credential access

Credentials name one project and one memory access mode. Ordinary issuance defaults to read/write. To issue a reader, use the operator connection and put flags after the name:

```sh
taisce credential issue reporting-reader --project research --read-only
```

The command prints the token once. Store it as a secret. Both access modes use opaque `tsk_` tokens.
The protected registry determines authority; token text does not encode permissions. Configure
secret scanning and redaction for the `tsk_` prefix.

| Operation | Read-only | Read/write |
|---|---|---|
| Recall and historical recall | Allowed | Allowed |
| Record inventory and history | Allowed | Allowed |
| Citation inspection and freshness | Allowed | Allowed |
| Subject export within the project | Allowed | Allowed |
| Observation ingestion | Forbidden | Allowed |
| Subject erasure | Forbidden | Allowed |

A forbidden operation returns `403 forbidden` before its body is parsed or executed. The audit log
records the credential, project, operation, refused outcome and zero affected records. Read requests
still create operational and audit records. Read-only access does not restrict which subjects the
credential can read within its project; it is not an end-user identity or an anonymization policy.

The operator commands support issuance and revocation; revoke and issue a new credential when a
different access mode is needed. Revocation and project suspension apply to both modes. Only the
operator connection can write registry metadata; the serving registry connection is read-only, and
the data-plane connection cannot access it.

Taisce is greenfield; there is no mixed-version server compatibility contract. Fresh bootstrap
installs the access column and its constraints. [D79](01-decisions.md) records the design and
remaining authorization scope.

## Operator credentials

An operator credential (D133) is the second kind. It reaches the management surface, `/manage/v1`,
served by the `manage` role, and no project's memory; a project credential is refused at that door
the way a stranger is. `taisce bootstrap` mints the first one when none is live, and
`taisce operator issue <name>` mints another over the administrative connection. With
`TAISCE_MANAGE_API` and `TAISCE_OPERATOR_TOKEN` set, `taisce project`, `taisce credential` and
`taisce audit` speak the surface and need no database connection:

```sh
TAISCE_MANAGE_API=http://127.0.0.1:8081 TAISCE_OPERATOR_TOKEN=… taisce credential issue app --project research --read-only
TAISCE_MANAGE_API=http://127.0.0.1:8081 TAISCE_OPERATOR_TOKEN=… taisce credential list --project research
```

The listing names each credential by id, name, token prefix and project; the token itself is shown
once at issue and never again.
