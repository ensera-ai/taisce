<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- The documentation site's navigation. Every published document in docs/ is listed here, and the
     build refuses one that is not. The generated reference is spliced in at the marker. -->

# Summary

[Welcome](introduction.md)

# Start

- [How it works](start/how-it-works.md)
- [Quickstart](developers/quickstart.md)
- [Examples](examples/overview.md)
  - [Remember and recall](examples/remember-and-recall.md)
  - [Answer with evidence](examples/answer-with-evidence.md)
  - [Forget a person](examples/forget-a-person.md)
  - [Long conversations](examples/long-conversations.md)
  - [Memory for a coding agent](examples/coding-agent-memory.md)

# Guides

- [Python](developers/python.md)
- [Java](developers/java.md)
- [.NET](developers/dotnet.md)
- [How adapters work](developers/adapters.md)
- [HTTP API](developers/http-api.md)
- [MCP and Claude Code](developers/mcp.md)
- [Command line](developers/cli.md)
- [Troubleshooting](developers/troubleshooting.md)

# Features

- [Recall options](12-recall-controls.md)
- [Citations](08-citation-resolution.md)
- [Looking back in time](09-temporal-history.md)
- [Browsing memory](11-record-inspection.md)
- [Looking up an entity](29-entity-inspection.md)
- [Long conversations, in depth](33-compaction.md)
- [Writing a record yourself](17-authored-assertions.md)
- [Agent state and files](18-agent-artifacts.md)
- [Ingesting documents](32-document-ingestion.md)
- [Notifications](34-notifications.md)
- [Correcting a record](14-record-corrections.md)
- [Retracting a record](13-record-retractions.md)
- [Reporting a doubt](35-feedback.md)
- [People and external IDs](19-subject-registry.md)
- [Recovering facts](15-fact-recovery.md)
- [Rebuilding facts](20-generation-rebuild.md)
- [API keys](10-project-credentials.md)
- [Separating content by project](16-content-access.md)
- [Health checks](07-operational-health.md)
- [Backlog limits](06-ingestion-budget.md)
- [Message embeddings](22-message-embeddings.md)
- [Theme embeddings](25-report-embeddings.md)
- [Embedding limits](30-embedding-boundary.md)
- [Terminal output](24-cli-presentation.md)

# Going deeper

- [Architecture](architecture/overview.md)
  - [Deployment](architecture/deployment.md)
  - [The write path](architecture/write-path.md)
  - [Formation](architecture/formation.md)
  - [The read path](architecture/read-path.md)
  - [Changing and forgetting](architecture/governance.md)
  - [Security](architecture/security.md)
- [PostgreSQL](postgresql/overview.md)
  - [Migrations](postgresql/migrations.md)
  - [Roles and grants](postgresql/roles-and-grants.md)
  - [Data model](postgresql/data-model.md)
  - [Concurrency](postgresql/concurrency.md)
  - [Indexing and plans](postgresql/indexing-and-plans.md)
  - [Running in production](21-production-postgresql.md)
- [Measurements](28-anchor-query-plans.md)
  - [GPU qualification, 2026-09-09](23-gpu-qualification.md)
  - [Report quality, 2026-09-09](26-report-quality-qualification.md)
  - [Vector layout, 2026-09-10](27-vector-layout-qualification.md)
  - [Vocabulary coverage, 2026-09-10](31-vocabulary-coverage-ap-news.md)
  - [Extraction models, 2026-09-11](36-extraction-models.md)

# Project

- [Individual contributor agreement](cla/individual.md)
- [Corporate contributor agreement](cla/corporate.md)

# Reference

<!-- generated reference -->
