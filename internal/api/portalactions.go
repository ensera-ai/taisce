// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// ── What the portal may do, and what it may not ───────────────────────────────────────────────
//
// The portal was built to show and not to act, with unparking and revocation named as left out.
// The result was that an operator who saw a parked turn read the page and then opened a terminal to
// retype what the page had just told them.
//
// Every action here is a management operation that already existed with a ledger name. Nothing new
// can be done through the portal that could not be done through the management API with the same
// credential — the page is a second way to reach the same operations, not a second set of powers.
//
// ── WHY ENTITY PURGE IS NOT HERE, THOUGH IT WAS ASKED FOR ────────────────────────────────────
//
// It cannot be. Purging an entity is a memory operation and needs a project credential; the operator
// credential deliberately cannot read or write memory, which is the boundary that lets an
// operator run this system without being able to read what is in it. Adding a purge button means
// giving the operator credential memory authority, and that trade — a boundary the whole design
// rests on, for one button — is not one this makes.
//
// The same reasoning removes anything else that reads or writes a person's records from these
// pages. An operator sees counts, receipts and identifiers, and never words.
//
// ── WHY AN IRREVERSIBLE ACTION ASKS FOR THE THING IT WILL DESTROY ────────────────────────────
//
// Revoking a credential cannot be undone by the page: the token is gone and every client holding it
// stops working. A confirmation checkbox is a thing people click. Typing the identifier back is a
// thing people only do when they meant to, and it is the same shape the CLI uses for the same
// reason.

// portalAction is one thing the portal can do, and what it costs to be wrong about it.
type portalAction struct {
	// name is the ledger's name for the operation, so an action here and a call to the management
	// API are one identifier in the record rather than two that have to be correlated.
	name string
	path string
	// confirm is the form field whose value must equal the target's identifier before an
	// irreversible action runs. Empty for anything the operator can simply do again.
	confirm string
	// recordedByStore is set for an action whose store writes its ledger row in the same transaction
	// as the change, so the row and the change cannot disagree. perform then records only a refusal
	// the store never saw. Recording the outcome again would count one operation twice on the
	// overview and in the breakdown (#57).
	recordedByStore bool
	run             func(*Portal, *http.Request, credential.Grant) (string, string, error)
}

// portalActions is the set, declared as data so a test can walk it — the same reason the memory
// surface's operations are a table. An action added without a ledger name, or without the confirm
// rule its reversibility calls for, is caught by a test rather than by an operator.
var portalActions = []portalAction{
	{name: domain.AuditProjectCreate, path: "/actions/create", run: (*Portal).create},
	{name: domain.AuditProjectSuspend, path: "/actions/suspend", run: (*Portal).suspend},
	{name: domain.AuditProjectResume, path: "/actions/resume", run: (*Portal).resume},
	{name: domain.AuditFormationUnpark, path: "/actions/unpark", recordedByStore: true, run: (*Portal).unpark},
	{name: domain.AuditCredentialIssue, path: "/actions/issue", run: (*Portal).issue},
	// Irreversible: the token is gone and every client holding it stops.
	{name: domain.AuditCredentialRevoke, path: "/actions/revoke", confirm: "id", run: (*Portal).revoke},
	{name: domain.AuditAuditSeal, path: "/actions/seal", run: (*Portal).seal},
}

// mountActions registers every declared action. One loop, so a route cannot exist without its
// declaration and its declaration cannot exist without a route.
func (p *Portal) mountActions(mux *http.ServeMux) {
	for _, action := range portalActions {
		action := action
		mux.HandleFunc("POST "+PortalPrefix+action.path, p.acting(action.name,
			func(w http.ResponseWriter, r *http.Request, grant credential.Grant, session string) {
				p.perform(w, r, grant, session, action)
			}))
	}
}

// perform runs one action, records it, and carries its outcome to the page that follows.
func (p *Portal) perform(w http.ResponseWriter, r *http.Request, grant credential.Grant, session string, action portalAction) {
	// The project is recorded only once it is known to be one. A form field is caller-controlled
	// text, and the ledger is permanent and sealed: writing "../p1" or a sentence into its project
	// column is the same failure the anonymous-audit test exists to prevent, arriving through a
	// signed-in page instead. An unusable name is recorded as no project rather than as whatever
	// was posted.
	recorded := recordableProject(r.PostFormValue("project"))
	if action.confirm != "" {
		// The typed confirmation must equal the identifier being acted on. Compared here rather
		// than inside each action, so an irreversible action cannot be added without one.
		if strings.TrimSpace(r.PostFormValue("confirm")) != strings.TrimSpace(r.PostFormValue(action.confirm)) ||
			strings.TrimSpace(r.PostFormValue(action.confirm)) == "" {
			p.m.record(r, action.name, grant, recorded, domain.OutcomeRefused, 0)
			p.remember(session, portalOutcome{Action: action.name,
				Detail: "type the identifier exactly to confirm; nothing was changed"})
			http.Redirect(w, r, p.back(r), http.StatusSeeOther)
			return
		}
	}
	_, detail, err := action.run(p, r, grant)
	// The store has already written this outcome when it records for itself and it either succeeded
	// or refused on the record. A refusal before the store was reached, or a store failure that rolled
	// its transaction back, left no row, so perform writes that one.
	var onRecord recordedRefusal
	storeRecorded := action.recordedByStore && (err == nil || errors.As(err, &onRecord))
	if err != nil {
		if !storeRecorded {
			p.m.record(r, action.name, grant, recorded, domain.OutcomeRefused, 0)
		}
		// A target the action cannot act on is the caller's mistake, not the system failing, and an
		// ERROR for every bad form post would bury the failures that are real. Only a store failure
		// is logged, and then only the operation and a project that validated — never the posted
		// text, and never the store's error, which is how a table name or a row reaches a log.
		if !errors.Is(err, errInvalidPortalTarget) {
			p.m.log.Error("portal action failed", "operation", action.name, "project", recorded)
		}
		p.remember(session, portalOutcome{Action: action.name, Detail: "it did not work; nothing was changed"})
		http.Redirect(w, r, p.back(r), http.StatusSeeOther)
		return
	}
	if !storeRecorded {
		p.m.record(r, action.name, grant, recorded, domain.OutcomeAllowed, 1)
	}
	p.remember(session, portalOutcome{Done: true, Action: action.name, Detail: detail})
	http.Redirect(w, r, p.back(r), http.StatusSeeOther)
}

// back is where the operator is sent afterwards: the project page they acted from, or the instance
// page. Built from the form's project field and validated as a project name, never taken from a
// referer or a redirect parameter — either of those is a caller choosing where a signed-in browser
// goes next.
func (p *Portal) back(r *http.Request) string {
	name := strings.TrimSpace(r.PostFormValue("project"))
	if name == "" || !validProjectName(name) {
		return PortalPrefix + "/"
	}
	return PortalPrefix + "/projects/" + name
}

// create provisions a project's storage. Reversible in the sense that matters: an empty project
// can be suspended and ignored, and nothing anybody owns is at stake in one existing.
func (p *Portal) create(r *http.Request, _ credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	if !validProjectName(name) {
		return name, "", errInvalidPortalTarget
	}
	if p.m.stores.Provision == nil {
		return name, "", errInvalidPortalTarget
	}
	return name, "the project exists and is accepting writes", p.m.stores.Provision(r.Context(), name)
}

func (p *Portal) suspend(r *http.Request, _ credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	if !validProjectName(name) {
		return name, "", errInvalidPortalTarget
	}
	return name, "the project is suspended and accepts no writes", p.m.stores.Projects.Suspend(r.Context(), name)
}

func (p *Portal) resume(r *http.Request, _ credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	if !validProjectName(name) {
		return name, "", errInvalidPortalTarget
	}
	return name, "the project is accepting writes again", p.m.stores.Projects.Resume(r.Context(), name)
}

func (p *Portal) unpark(r *http.Request, grant credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	observation := strings.TrimSpace(r.PostFormValue("observation"))
	if !validProjectName(name) || !validUUID(observation) {
		return name, "", errInvalidPortalTarget
	}
	changed, err := p.m.stores.Observations.UnparkAudited(r.Context(), p.m.schema, name, observation, grant.CredentialID)
	if errors.Is(err, pg.ErrFormationTurnNotFound) {
		// A turn that is not there is the caller naming something that does not exist, not the
		// system failing — one refusal shape with every other bad target, so an operator guessing
		// at identifiers learns nothing from which refusal came back. The store has already recorded
		// the refusal, in the transaction that looked for the turn.
		return name, "", recordedRefusal{errInvalidPortalTarget}
	}
	if err != nil {
		return name, "", err
	}
	if !changed {
		// Not an error: the turn was already unparked, by another operator or by the driver. Saying
		// so is more useful than a success message about work that did not happen.
		return name, "that turn was not parked; nothing changed", nil
	}
	return name, "the turn is queued for formation again", nil
}

func (p *Portal) issue(r *http.Request, _ credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	label := strings.TrimSpace(r.PostFormValue("name"))
	if !validProjectName(name) || label == "" || len(label) > 128 {
		return name, "", errInvalidPortalTarget
	}
	access := credential.ReadWrite
	if r.PostFormValue("access") == "read_only" {
		access = credential.ReadOnly
	}
	// The token is deliberately not carried to the page. It is shown once, by the CLI, to whoever
	// ran it; a token rendered into a browser page is a token in a screenshot, a scroll buffer and
	// a printer queue. The portal says a credential exists and what it may do.
	_, _, err := p.m.issueProjectCredential(r.Context(), label, name, access)
	if errors.Is(err, pg.ErrNoSuchProject) {
		// A project that does not exist, or is suspended, gets the same refusal as a malformed name,
		// as every other target on these pages does.
		return name, "", errInvalidPortalTarget
	}
	if err != nil {
		return name, "", err
	}
	return name, "the credential exists; its token was not shown here and cannot be recovered — issue from the CLI if you need it", nil
}

func (p *Portal) revoke(r *http.Request, _ credential.Grant) (string, string, error) {
	name := strings.TrimSpace(r.PostFormValue("project"))
	id := strings.TrimSpace(r.PostFormValue("id"))
	if !validProjectName(name) || !validUUID(id) {
		return name, "", errInvalidPortalTarget
	}
	// The form sits on one project's page, so it revokes only that project's keys. Otherwise a post
	// from one project's page could revoke another project's key, recorded on the ledger under the
	// wrong project, or an operator key, including the one signed in (#52). The management API and the
	// CLI revoke across the instance, because their caller names a key, not a page.
	err := p.m.credentials.RevokeInProject(r.Context(), id, name)
	if errors.Is(err, credential.ErrUnknown) {
		return name, "", errInvalidPortalTarget
	}
	if err != nil {
		return name, "", err
	}
	return name, "the credential is revoked; every client holding it stops now", nil
}

func (p *Portal) seal(r *http.Request, _ credential.Grant) (string, string, error) {
	seal, err := p.m.stores.Audit.Seal(r.Context())
	if err != nil {
		return "", "", err
	}
	// The substrate seals nothing when every entry is already under a seal, and says so with an empty
	// seal rather than an error. Reporting "sealed" then would describe work that did not happen (#55).
	if seal.Entries == 0 {
		return "", "nothing new to seal; every entry was already under a seal", nil
	}
	return "", fmt.Sprintf("the ledger is sealed to entry %d; this seal covers %d entries", seal.To, seal.Entries), nil
}

// errInvalidPortalTarget is a form naming something this action cannot act on.
//
// One error for every shape of bad input, because the page says the same thing to all of them: a
// refusal that distinguished "no such project" from "not a project name" would answer a question
// about what exists to whoever is guessing.
var errInvalidPortalTarget = errors.New("the form named something this action cannot act on")

// recordedRefusal is a refusal the store has already written to the ledger, so perform does not write
// it again. It unwraps to the refusal, so every check on what kind of refusal it was still holds.
type recordedRefusal struct{ error }

func (e recordedRefusal) Unwrap() error { return e.error }

// recordableProject is the posted project name if it is a valid one, and empty otherwise — the only
// form of a caller-supplied project that may reach the ledger or a log.
func recordableProject(posted string) string {
	name := strings.TrimSpace(posted)
	if !validProjectName(name) {
		return ""
	}
	return name
}

// validProjectName accepts what a project is allowed to be called, and nothing else.
//
// Checked here rather than trusted from the page. Every one of these values is interpolated into a
// redirect and passed to a store that will use it as a scope, and a form field is a thing anybody
// can post — the page it came from does not make it safe.
func validProjectName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// validUUID accepts the canonical form and nothing else, so an identifier reaching a store is one
// shape rather than whatever a form posted.
func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == strings.ToLower(value)
}

// PortalActionCount is how many actions the portal declares.
//
// Exported for the test that walks them. The memory surface's guard tests enumerate its operations
// for the same reason: a set that is checked one entry at a time grows an entry nobody checks, and
// the entry nobody checks is the one whose ledger row is missing.
func PortalActionCount() int { return len(portalActions) }
