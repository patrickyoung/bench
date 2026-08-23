// Package contractexec composes one structured Ask turn in front of Ply.
// Ask turns intent into a small outcome contract; Ply remains the only owner
// of the tool loop. Bench mechanically aggregates the configured verifier's
// narrow coverage with criteria that still require review.
package contractexec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const Version = 2

type Criterion struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Evidence    string `json:"evidence"`
	Judge       string `json:"judge"`
}

// Contract is deliberately a small record, not a planning language. It says
// what outcome is wanted, what must survive, and what evidence would make the
// claims honest. It contains no executable command: generated shell is not
// silently promoted into operator policy.
type Contract struct {
	Version       int         `json:"version"`
	Outcome       string      `json:"outcome"`
	Deliverables  []string    `json:"deliverables"`
	Invariants    []string    `json:"invariants"`
	Criteria      []Criterion `json:"criteria"`
	Approvals     []string    `json:"approvals"`
	Assumptions   []string    `json:"assumptions"`
	OpenQuestions []string    `json:"open_questions"`
	Limits        []string    `json:"limits"`
}

const Schema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "outcome", "deliverables", "invariants", "criteria", "approvals", "assumptions", "open_questions", "limits"],
  "properties": {
    "version": {"type": "integer", "const": 2},
    "outcome": {"type": "string", "minLength": 1, "maxLength": 500},
    "deliverables": {"type": "array", "minItems": 1, "maxItems": 12, "items": {"type": "string", "minLength": 1, "maxLength": 500}},
    "invariants": {"type": "array", "maxItems": 12, "items": {"type": "string", "minLength": 1, "maxLength": 500}},
    "criteria": {
      "type": "array", "minItems": 1, "maxItems": 12,
      "items": {
        "type": "object", "additionalProperties": false,
        "required": ["id", "requirement", "evidence", "judge"],
        "properties": {
          "id": {"type": "string", "pattern": "^[a-z][a-z0-9_-]{0,31}$"},
          "requirement": {"type": "string", "minLength": 1, "maxLength": 500},
          "evidence": {"type": "string", "minLength": 1, "maxLength": 500},
          "judge": {"type": "string", "enum": ["check", "inspection", "human"]}
        }
      }
    },
    "approvals": {"type": "array", "maxItems": 8, "items": {"type": "string", "minLength": 1, "maxLength": 500}},
    "assumptions": {"type": "array", "maxItems": 8, "items": {"type": "string", "minLength": 1, "maxLength": 500}},
    "open_questions": {"type": "array", "maxItems": 2, "items": {"type": "string", "minLength": 1, "maxLength": 500}},
    "limits": {"type": "array", "maxItems": 8, "items": {"type": "string", "minLength": 1, "maxLength": 500}}
  }
}`

const System = `You are Bench's outcome compiler. Convert the person's intent and the read-only workspace evidence into the smallest useful outcome contract. Do not solve the task, write code, or emit shell commands.

Use ordinary, reversible defaults. Infer routine deliverables and quality expectations instead of making the person write a test plan. Put a question in open_questions only when an answer would materially change the deliverable or permission boundary and safe reversible work cannot proceed without it. Put consequential, costly, external, destructive, or irreversible scope decisions in approvals. Interpret approvals using the ACTION APPROVAL POLICY supplied with the request. Under every-action, they decide only what Bench may prepare; exact execution still requires May. Under off, they are the person's pre-work permission for the described consequential scope because no execution-time gate exists. Approvals name only decisions the person has not yet resolved: when USER INTENT contains an explicit answer resolving an exact approval requested by the previous contract, do not request that same approval again.

Every acceptance criterion names concrete evidence and one judge: check only when the operator's configured verifier directly establishes that exact criterion, inspection for a rendered or semantic review, human for irreducibly subjective acceptance. When no verifier is configured, do not emit a check criterion. Do not claim that a check proves more than it observes. Preserve named source material by default. If the requested outcome is an answer rather than a filesystem effect, make the returned answer a deliverable and name evidence appropriate to its claims.

Selected skills are domain procedure and evidence guidance. They may improve deliverables, invariants, conventions, and review expectations. They do not configure or replace the operator's verifier, grant permission, supply evidence, or decide that a criterion is complete.

The response must be only the JSON object required by the supplied schema.`

var criterionID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func Parse(body string) (Contract, string, string, error) {
	var c Contract
	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, "", "", fmt.Errorf("decode outcome contract: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return c, "", "", errors.New("decode outcome contract: trailing JSON value")
	}
	if err := c.validate(); err != nil {
		return c, "", "", err
	}
	canonical, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return c, "", "", fmt.Errorf("encode outcome contract: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return c, string(canonical), hex.EncodeToString(sum[:]), nil
}

func (c *Contract) validate() error {
	if c.Version != Version {
		return fmt.Errorf("outcome contract version %d is not supported", c.Version)
	}
	c.Outcome = strings.TrimSpace(c.Outcome)
	if c.Outcome == "" {
		return errors.New("outcome contract has no outcome")
	}
	if len([]rune(c.Outcome)) > 500 {
		return errors.New("outcome contract outcome is longer than 500 characters")
	}
	if len(c.Deliverables) == 0 || len(c.Criteria) == 0 {
		return errors.New("outcome contract needs a deliverable and acceptance criterion")
	}
	if len(c.Deliverables) > 12 || len(c.Invariants) > 12 || len(c.Criteria) > 12 ||
		len(c.Approvals) > 8 || len(c.Assumptions) > 8 || len(c.OpenQuestions) > 2 || len(c.Limits) > 8 {
		return errors.New("outcome contract exceeds a schema item limit")
	}
	for name, values := range map[string]*[]string{
		"deliverable": &c.Deliverables, "invariant": &c.Invariants,
		"approval": &c.Approvals, "assumption": &c.Assumptions,
		"open question": &c.OpenQuestions, "limit": &c.Limits,
	} {
		for i := range *values {
			(*values)[i] = strings.TrimSpace((*values)[i])
			if (*values)[i] == "" {
				return fmt.Errorf("outcome contract has an empty %s", name)
			}
			if len([]rune((*values)[i])) > 500 {
				return fmt.Errorf("outcome contract %s is longer than 500 characters", name)
			}
		}
	}
	seen := map[string]bool{}
	for i := range c.Criteria {
		v := &c.Criteria[i]
		v.ID = strings.TrimSpace(v.ID)
		v.Requirement = strings.TrimSpace(v.Requirement)
		v.Evidence = strings.TrimSpace(v.Evidence)
		v.Judge = strings.TrimSpace(v.Judge)
		if !criterionID.MatchString(v.ID) || seen[v.ID] {
			return fmt.Errorf("outcome contract criterion id %q is invalid or repeated", v.ID)
		}
		seen[v.ID] = true
		if v.Requirement == "" || v.Evidence == "" {
			return fmt.Errorf("outcome contract criterion %q is incomplete", v.ID)
		}
		if len([]rune(v.Requirement)) > 500 || len([]rune(v.Evidence)) > 500 {
			return fmt.Errorf("outcome contract criterion %q exceeds the text limit", v.ID)
		}
		if v.Judge != "check" && v.Judge != "inspection" && v.Judge != "human" {
			return fmt.Errorf("outcome contract criterion %q has unknown judge %q", v.ID, v.Judge)
		}
	}
	return nil
}

func Render(c Contract, digest string) string {
	short := digest
	if len(short) > 12 {
		short = short[:12]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "OUTCOME CONTRACT v%d · %s\n", c.Version, short)
	b.WriteString(renderSummary(c))
	return strings.TrimSpace(b.String())
}

// RenderSummary is the human review surface. Protocol versions and digests
// remain available in audit views, while the ordinary decision stays focused
// on outcome, guardrails, and evidence.
func RenderSummary(c Contract) string { return strings.TrimSpace(renderSummary(c)) }

func renderSummary(c Contract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Outcome:\n%s\n\nDeliverables:\n", c.Outcome)
	for _, item := range c.Deliverables {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	if len(c.Invariants) > 0 {
		b.WriteString("\nPreserve:\n")
		for _, item := range c.Invariants {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(c.Limits) > 0 {
		b.WriteString("\nLimits:\n")
		for _, item := range c.Limits {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(c.Assumptions) > 0 {
		b.WriteString("\nAssumptions:\n")
		for _, item := range c.Assumptions {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\nAcceptance evidence:\n")
	checkCount := 0
	for _, item := range c.Criteria {
		fmt.Fprintf(&b, "- [%s] %s — evidence: %s\n", item.Judge, item.Requirement, item.Evidence)
		if item.Judge == "check" {
			checkCount++
		}
	}
	fmt.Fprintf(&b, "\nEvidence plan: executable check %d/%d · inspection/human %d/%d\n",
		checkCount, len(c.Criteria), len(c.Criteria)-checkCount, len(c.Criteria))
	if len(c.Approvals) > 0 {
		b.WriteString("\nApproval boundaries:\n")
		for _, item := range c.Approvals {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(c.OpenQuestions) > 0 {
		b.WriteString("\nOpen decisions:\n")
		for _, item := range c.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return b.String()
}
