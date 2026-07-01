package orchestrator

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/prompts"
)

// personaMarker is a stable phrase from internal/prompts/defaults/persona.md.
const personaMarker = "Contract first"

// rulesMarker is a stable phrase from internal/prompts/defaults/rules.md.
const rulesMarker = "The Loop"

// TestSystemPromptPrependsPersona proves SapaLOQ's shared persona is woven into
// every role's system prompt, while the role's own instructions are preserved.
func TestSystemPromptPrependsPersona(t *testing.T) {
	o := &Orchestrator{} // nil prompt manager → embedded defaults via rolePrompt

	cases := []struct {
		role       string
		roleMarker string // a phrase unique to that role's own prompt
	}{
		{prompts.RoleOrchestrator, "SapaLOQ's orchestrator"},
		{prompts.RolePlanner, "planner"},
		{prompts.RoleAgent, "executor"},
		{prompts.RoleScribe, "scribe"},
	}
	for _, tc := range cases {
		got := o.systemPrompt(tc.role)
		if !strings.Contains(got, personaMarker) {
			t.Fatalf("systemPrompt(%q) missing persona marker %q", tc.role, personaMarker)
		}
		if !strings.Contains(got, tc.roleMarker) {
			t.Fatalf("systemPrompt(%q) lost its role content (marker %q)", tc.role, tc.roleMarker)
		}
		// Persona must come first (it is the baseline character), role after.
		if pi, ri := strings.Index(got, personaMarker), strings.Index(got, tc.roleMarker); pi > ri {
			t.Fatalf("systemPrompt(%q): persona should precede role content (persona@%d role@%d)", tc.role, pi, ri)
		}
	}
}

// TestSystemPromptComposesPersonaRulesRole proves the shared layers are woven
// into every role's system prompt in the order persona → rules → role, with the
// role's own instructions preserved.
func TestSystemPromptComposesPersonaRulesRole(t *testing.T) {
	o := &Orchestrator{} // nil prompt manager → embedded defaults via rolePrompt

	cases := []struct {
		role       string
		roleMarker string // a phrase unique to that role's own prompt
	}{
		{prompts.RoleOrchestrator, "SapaLOQ's orchestrator"},
		{prompts.RolePlanner, "planner"},
		{prompts.RoleAgent, "executor"},
		{prompts.RoleScribe, "personal storage"},
	}
	for _, tc := range cases {
		got := o.systemPrompt(tc.role)
		pi := strings.Index(got, personaMarker)
		ri := strings.Index(got, rulesMarker)
		ci := strings.Index(got, tc.roleMarker)
		if pi < 0 {
			t.Fatalf("systemPrompt(%q) missing persona marker %q", tc.role, personaMarker)
		}
		if ri < 0 {
			t.Fatalf("systemPrompt(%q) missing rules marker %q", tc.role, rulesMarker)
		}
		if ci < 0 {
			t.Fatalf("systemPrompt(%q) lost its role content (marker %q)", tc.role, tc.roleMarker)
		}
		// Order must be persona → rules → role.
		if !(pi < ri && ri < ci) {
			t.Fatalf("systemPrompt(%q): expected order persona<rules<role (persona@%d rules@%d role@%d)", tc.role, pi, ri, ci)
		}
	}
}

// TestSystemPromptRulesNotDoubleWrapped proves asking for the rules role itself
// returns the bare rules layer, not the shared layers prepended to it.
func TestSystemPromptRulesNotDoubleWrapped(t *testing.T) {
	o := &Orchestrator{}
	got := o.systemPrompt(prompts.RoleRules)
	if strings.Contains(got, personaMarker) {
		t.Fatalf("rules role should not carry the persona layer")
	}
	if got != prompts.Default(prompts.RoleRules) {
		t.Fatalf("systemPrompt(rules) should equal the bare rules default")
	}
}

// TestSystemPromptPersonaNotDoubleWrapped proves asking for the persona role
// itself returns the bare persona, not the persona prepended to itself.
func TestSystemPromptPersonaNotDoubleWrapped(t *testing.T) {
	o := &Orchestrator{}
	got := o.systemPrompt(prompts.RolePersona)
	if strings.Count(got, personaMarker) != 1 {
		t.Fatalf("persona role should contain the persona exactly once; got %d occurrences", strings.Count(got, personaMarker))
	}
	if got != prompts.Default(prompts.RolePersona) {
		t.Fatalf("systemPrompt(persona) should equal the bare persona default")
	}
}
