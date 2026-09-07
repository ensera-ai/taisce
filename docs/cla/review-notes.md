# Questions for legal review

The contributor agreements in this directory are modelled on short, plain-language templates and have
not been reviewed by a lawyer. Two questions are beyond what an engineer should decide, and both
affect whether the agreements do what they are meant to.

## 1. Is the establishment a separate legal person?

The counterparty is named as **Baserat Al Mustaqbal, an establishment registered in Jordan, trading as
Ensera**.

A contract needs a counterparty capable of holding it. In several jurisdictions an *establishment*
(مؤسسة فردية) is a registered trading name for a sole proprietor rather than a legal person distinct
from its owner — in which case the agreement is in substance with the owner personally, and rights
granted to it are held personally.

That may be perfectly acceptable. What it affects is what happens on a sale, a change of owner, or
incorporation into a company: rights held by a person do not automatically move with a business. Since
the whole purpose of taking an agreement rather than a sign-off is **chain of title** (D45), this is
the question that decides whether it achieves it.

**If incorporation is planned**, whether to wait, or to include an assignment clause covering a future
successor entity, is worth settling before the first external contribution rather than after.

## 2. Governing law, and whether to state one

Neither document names a governing law or a forum. That is deliberate — it is a choice, not an
omission.

**Naming Jordan** is the natural answer for a Jordanian entity and is the one most likely to be
enforceable at home. It also asks an international contributor to accept a jurisdiction they have no
connection to, which some individuals and most corporate legal departments will decline or escalate —
friction on precisely the contributions the project wants.

**Naming nothing** leaves it to conflict-of-laws rules, which is uncertain but rarely tested in
practice: contributor agreements are almost never litigated, and their practical value is evidential
rather than as a thing to sue on.

Widely used agreements differ here, so it is a real choice rather than a settled one.

## Also worth confirming

- Whether the **patent licence** in section 3 is enforceable as drafted under Jordanian law.
- Whether the retention statement in section 8 is adequate as a **privacy notice** for contributors in
  the EU and the UK, given that signatures are kept indefinitely and cannot be erased on request
  without removing the Contribution. The stated basis is that the record is the evidence of the grant.
- Whether the **copyright notice** on source files should name Ensera, "The Taisce Authors", or
  individual contributors, given that this agreement is a licence and not an assignment — contributors
  keep their copyright, so a blanket notice naming only Ensera stops being accurate the moment
  somebody else contributes. This one blocks #78.
