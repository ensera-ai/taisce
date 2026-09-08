<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Project credential access

Credentials name one project and one memory access mode. Existing credentials and ordinary issuance
remain read/write. To issue a reader, use the operator connection and put flags after the name:

```sh
taisce credential issue reporting-reader --project research --read-only
```

The command prints the token once. Store it as a secret. Read-only keys begin with `tsr_`; writable
keys begin with `tsk_`. Clients must treat the whole value as opaque. Recognize both prefixes when
configuring secret scanning and redaction.

| Operation | Read-only | Read/write |
|---|---|---|
| Recall and historical recall | Allowed | Allowed |
| Citation inspection and freshness | Allowed | Allowed |
| Subject export within the project | Allowed | Allowed |
| Observation ingestion | Forbidden | Allowed |
| Subject erasure | Forbidden | Allowed |

A forbidden operation returns `403 forbidden` before its body is parsed or executed. The audit log
records the credential, project, operation, refused outcome and zero affected records. Read requests
still create operational and audit records. Read-only access does not restrict which subjects the
credential can read within its project; it is not an end-user identity or an anonymization policy.

Revoke and issue a new credential to change access. Editing the stored mode cannot elevate an issued
key: its token marker must match the recorded mode. Revocation and project suspension retain their
existing behavior for both modes.

Control migration 0004 must be applied for the new server. During a mixed-version rollout, older
servers refuse read-only keys with 401 because they do not recognize the prefix; they cannot grant
those keys write access. Existing writable keys continue to work. No data-plane connection gains
registry write privileges. [D79](01-decisions.md) records the design and remaining authorization scope.
