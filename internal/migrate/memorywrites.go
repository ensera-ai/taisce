// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"sort"
	"strings"
)

// memoryWrites is every memory-namespace table the memory role may change, and how.
//
// An allowlist, and the only one. grantMemoryToDataPlane grants exactly this and nothing more, and
// the admission check refuses a connection whose role can change anything else. So a table a
// migration adds is readable and not writable by the memory service until somebody lists it here:
// the first write to it fails, in a test, where before every new table was writable by default and
// keeping it otherwise depended on remembering two hand-kept lists. Reading was never the risk this
// guards: the memory role reads the whole namespace.
//
// What is absent is absent on purpose:
//   - the policy catalogs and migration state (projection_kind, predicate, speaker_term,
//     unresolvable_term, schema_migration), which only reviewed migrations change;
//   - the embedding generations and their activation, and the ingestion and storage accounting,
//     which operator commands on the administrative connection change, or the definer functions
//     that account for a write;
//   - project, except one column: suspension, retention and surfaces belong to the management
//     surface, which holds the administrative connection. A compromised memory process must not
//     resume a suspended project or lengthen a retention. It keeps UPDATE on label alone (see
//     memoryColumnWrites);
//   - partitions: every write goes through the partitioned parent, which is where Postgres checks
//     the privilege.
//
// audit_entry is append-only in the grant as well as in its triggers, so the ledger's promise does
// not rest on a trigger alone. audit_seal is not writable at all: sealing is the substrate's
// audit_seal_now() (0064), so the memory role cannot write a digest of its own into the chain.
var memoryWrites = map[string][]string{
	"audit_entry": {"INSERT"},
	// A retention sweep runs in the worker, as the memory role, and writes the receipt for what it
	// deleted in the same transaction as the deletes. Append-only for the same reason as the
	// ledger: a receipt that can be rewritten is not evidence.
	"retention_sweep": {"INSERT"},
}

// memoryColumnWrites are privileges held on named columns only.
//
// project's label is here for a lock, not for the label. The entity-embedding triggers (0049, 0050)
// take `SELECT … FROM project … FOR UPDATE` as the caller, to serialise against a suspension, and
// Postgres grants a row lock only to a role holding UPDATE on at least one column. The cheapest
// column to give is the display name: the lock works, and the columns that govern the project stay
// out of reach. Found by TestRuntimeRelationshipsCannotCrossProjectsOrLeaveDanglingTargets, which
// failed the first time project was made read-only.
var memoryColumnWrites = map[string]map[string][]string{
	"project": {"UPDATE": {"label"}},
}

// memoryDML are the tables the memory service writes in the ordinary way.
var memoryDML = []string{
	"agent_artifact", "chunk", "community", "community_member", "community_report", "curated_claim",
	"data_subject", "embedding_build", "entity", "entity_embedding", "entity_embedding_build",
	"entity_embedding_source", "entity_embedding_target", "entity_name_receipt", "erasure_request",
	"fact", "fact_evidence", "fact_generation", "fact_generation_record", "fact_history",
	"fact_rebuild_job", "fact_receipt", "fact_receipt_history", "formation_health", "memory_feedback",
	"message_embedding", "notification_delivery", "notification_endpoint", "observation",
	"observation_retry", "projection_dependency", "record_retraction", "rejected_claim",
	"report_embedding", "report_embedding_build", "report_embedding_source", "report_embedding_target",
	"segment", "source_extraction", "subject_retry", "turn_message", "watermark",
}

func init() {
	for _, table := range memoryDML {
		memoryWrites[table] = []string{"INSERT", "UPDATE", "DELETE"}
	}
}

// memoryWriteTables is the list in a fixed order, so the grant is the same statements every run.
func memoryWriteTables() []string {
	tables := make([]string, 0, len(memoryWrites))
	for table := range memoryWrites {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

// memoryWriteGrants is the list as "table:PRIVILEGE" pairs, the form the admission check compares.
func memoryWriteGrants() []string {
	var out []string
	for _, table := range memoryWriteTables() {
		for _, privilege := range memoryWrites[table] {
			out = append(out, table+":"+strings.ToUpper(privilege))
		}
	}
	return out
}

// memoryColumnGrants is memoryColumnWrites as "table:PRIVILEGE:column" triples, the form the
// admission check compares.
func memoryColumnGrants() []string {
	var out []string
	for table, privileges := range memoryColumnWrites {
		for privilege, columns := range privileges {
			for _, column := range columns {
				out = append(out, table+":"+strings.ToUpper(privilege)+":"+column)
			}
		}
	}
	sort.Strings(out)
	return out
}
