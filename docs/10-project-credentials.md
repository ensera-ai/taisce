<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Credentials

A credential is a bearer token that opens one door. A project credential reaches one project's
memory, read-only or read/write. An operator credential reaches the management surface and no
project's memory.

## Issue a project credential

With the operator database connection (`TAISCE_ADMIN_DSN`) set, name the credential first and put
the flags after the name:

```sh
taisce credential issue reporting-reader --project research --read-only
```

```text
credential reporting-reader (7f3a…) reaches project research with read_only access
token: tsk_…

This token is shown once and cannot be recovered.
```

Leave out `--read-only` for a read/write credential. `--project` defaults to `default`. The project
must already exist; create it with `taisce project create research`.

Store the token as a secret. Every token starts with `tsk_`, which makes it easy to catch with
secret scanning and log redaction. The token itself carries no permissions. The registry decides
what it can do.

## What each mode allows

| Operation | Read-only | Read/write |
|---|---|---|
| Recall, including historical recall | Yes | Yes |
| Record inventory and history | Yes | Yes |
| Citation inspection and freshness | Yes | Yes |
| Artifact and subject get/list | Yes | Yes |
| Subject export | Yes | Yes |
| Sending observations | No | Yes |
| Asserting, correcting and retracting records | No | Yes |
| Artifact put/delete, subject register/update | No | Yes |
| Subject erasure | No | Yes |

A forbidden call returns `403 forbidden` before the request body is read. The audit log records the
refusal, with zero records affected.

## Revoke and replace

```sh
taisce credential revoke 7f3a…
```

You can't change a credential's mode. Revoke it and issue a new one. Revoking a credential and
suspending its project both work the same way for either mode.

## Operator credentials

An operator credential reaches `/manage/v1`, the management surface, and nothing else. A project
credential sent there is refused, just like a request with no credential.

`taisce bootstrap` mints the first operator credential if none exists. To mint another one, use the
operator database connection:

```sh
taisce operator issue alice
```

When `TAISCE_MANAGE_API` and `TAISCE_OPERATOR_TOKEN` are set, `taisce project`, `taisce credential`
and `taisce audit` go through the management surface and need no database connection:

```sh
export TAISCE_MANAGE_API=http://127.0.0.1:8081
export TAISCE_OPERATOR_TOKEN=tsk_…
taisce credential issue app --project research --read-only
taisce credential list --project research
```

`credential list` shows each credential's ID, name, token prefix, project and mode, plus a revoked
date if it has one. Leave out `--project` to list operator credentials. `list` is only available
through the management surface.

## Watch out for

- **Read-only is not a narrower view.** A read-only credential can still read every subject in its
  project. To keep content apart, use [separate projects](16-content-access.md).
- **A credential is not a user.** It identifies an application, not an end user. It is not an
  anonymization tool.
- **Reads leave a trail.** Read calls still write content-free audit entries.
- **Keep tokens out of shell history.** `TAISCE_OPERATOR_TOKEN` is read only from the environment,
  never from a flag, so it stays out of shell history and the process list.

## How it works

Credentials live in a registry schema that the memory service's database role cannot read. The
serving process reads it through a separate read-only role, and only the operator connection can
write it. Only a digest of each token is stored, which is why a lost token can't be recovered.
