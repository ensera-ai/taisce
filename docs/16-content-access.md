<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Keeping content apart with projects

A project is the unit of access in Taisce. Every credential for a project can read all of that
project's memory. To keep memory apart, put it in separate projects.

## Set up separate projects

```sh
taisce project create team
taisce project create restricted
taisce credential issue team-app --project team
taisce credential issue restricted-app --project restricted
taisce credential issue team-reader --project team --read-only
```

Give each application only the credentials for the projects it may see. Send the credential as a
bearer token on every request. The project always comes from the credential, never from the request
body.

## What stays separate

- Recall, citations, record inventory, history, artifacts, subjects and exports only see the
  credential's project.
- The same entity name or subject ID in two projects refers to two unrelated things.
- Looking up a record from another project returns the same `404 not_found` as an unknown record.
- Erasing a subject in one project leaves the other projects untouched.

## Watch out for

- **Read-only is not a clearance level.** It stops writes and erasures. It doesn't hide any content
  inside the project.
- **There are no per-record labels.** Taisce has no classification, clearance or relabeling inside a
  project. Request fields like `classification` or `clearance` are unknown fields and are refused.
- **Combining projects is your job.** An application with credentials for two projects can query
  each one, but Taisce won't join the answers. Any combined answer needs your own access rules.
  Context both projects need has to be stored in both.
- **Erasure is per project.** If you store one person in several projects, export and erase them in
  each one.
- **Keep tokens out of prompts and logs.**

## How it works

This is an application-level boundary. Every request is authenticated, and the credential decides
which project's rows the query can touch. Database administrators, and anyone with direct SQL access
to the memory schema, can see across projects. If you need separation at the infrastructure level,
run separate Taisce instances.

See [credentials](10-project-credentials.md) for issuing and revoking tokens.
