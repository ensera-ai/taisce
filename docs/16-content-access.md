<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Separate content access with projects

Every project credential can read that project's content. A read-only credential prevents writes and
erasures; it is not a narrower content clearance. Use distinct projects when applications or users
should see different sets of memory.

For example, an operator can create ordinary and restricted domains:

```sh
taisce project create team
taisce project create restricted
taisce credential issue team-app --project team
taisce credential issue restricted-app --project restricted
taisce credential issue team-reader --project team --read-only
```

Give each application only the credentials for its authorized domains. Send a bearer credential with
each request; request bodies cannot choose another project or elevate access using `classification`
or `clearance`. Those fields are unsupported and refused. Keep tokens out of prompts and logs.

Entity names and subject identifiers may be identical in two projects without joining their memory.
Recall, citations, inventory, history and export use the credential's project. A foreign record lookup
returns the same not-found result as an unknown record. Erasing a subject in one project leaves other
projects intact, so applications storing a person in several projects must manage each export/erasure.

An application authorized for both domains may query each with its corresponding credential. Any
combined answer then needs that application's own access policy; Taisce does not traverse the graph
across those separate requests. Expect common context to be stored separately where both domains need
it. There are no per-record labels, relabeling operations or credential clearances within a project.

This is an application boundary enforced by authenticated scope selection. Database administrators
and direct SQL roles with access to the shared memory schema have broader authority. Operators needing
infrastructure separation can run separate instances. D90 records the supported contract and costs.
