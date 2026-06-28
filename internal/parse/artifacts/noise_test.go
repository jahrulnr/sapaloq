package artifacts

import (
	"strings"
	"testing"
)

func TestIsModelResponseArtifactFinalFileContent(t *testing.T) {
	sample := "### Final file content: webapp/client/src/components/CommandPalette.jsx\nimport { useState } from 'react'\nexport function CommandPalette() { return null }\n"
	if !IsModelResponseArtifact(sample) {
		t.Fatal("Final file content header must be detected as artifact")
	}
	if got := StripModelResponseArtifact(sample); got != "" {
		t.Fatalf("StripModelResponseArtifact = %q, want empty", got)
	}
}

func TestIsModelResponseArtifactFinalDataJSON(t *testing.T) {
	sample := "### Final_data/cars/006687.json\n{\"url\":\"https://www.parkers.co.uk/citroen/ds5/\",\"title\":\"Citroen DS5 Review\",\"heading\":\"Citroen DS5 Convertible 5d (12 on) Review\"}"
	if !IsModelResponseArtifact(sample) {
		t.Fatal("Final_data JSON scrape dump must be detected as artifact")
	}
}

func TestIsModelResponseArtifactNormalReply(t *testing.T) {
	cases := []string{
		"Hey! How can I help?",
		"Hai! Ada yang bisa kubantu?",
		"Here is a short code snippet:\n```go\nfmt.Println(\"hi\")\n```",
	}
	for _, c := range cases {
		if IsModelResponseArtifact(c) {
			t.Fatalf("normal reply flagged as artifact: %q", c)
		}
	}
}

func TestIsThinkingConfabulation(t *testing.T) {
	sample := turnsThinkingSample()
	if !IsThinkingConfabulation(sample) {
		t.Fatal("multi-task thinking bleed must be detected")
	}
	if IsThinkingConfabulation("The user said heyy — I'll greet them briefly.") {
		t.Fatal("short in-context thinking must not be flagged")
	}
}

func turnsThinkingSample() string {
	return "The user wants me to troubleshoot an error when installing the Aether package.\n\n" +
		"The user wants to analyze 16S rRNA sequences for composition and diversity.\n\n" +
		"The user wants help with something related to a pull request for fixing Windows build issues."
}

func TestIsConversationalPing(t *testing.T) {
	if !IsConversationalPing("heyy") {
		t.Fatal("heyy must be conversational")
	}
	if IsConversationalPing("fix the bug in internal/foo.go") {
		t.Fatal("task-like message must not be conversational")
	}
}

func TestIsAutopilotEcho(t *testing.T) {
	echo := "SapaLOQ received: <sapaloq:autopilot>\nContinue the existing task\n</sapaloq:autopilot>"
	if !IsAutopilotEcho(echo) {
		t.Fatal("autopilot echo must be detected")
	}
	if IsAutopilotEcho("Reading plan.md and creating hero template.") {
		t.Fatal("normal narration must not be flagged as echo")
	}
}

func TestIsUnanchoredThinkingConfabulation(t *testing.T) {
	anchor := "Execute migration plan for banguninfo_devlog Drupal theme widgets"
	thinking := "The user wants help installing the faplus plugin for a FiveM server."
	if !IsUnanchoredThinkingConfabulation(thinking, anchor) {
		t.Fatal("unrelated FiveM thinking must be dropped for Drupal task")
	}
	grounded := "The user wants to port banguninfo_devlog hero_widget templates from bangunsoft."
	if IsUnanchoredThinkingConfabulation(grounded, anchor) {
		t.Fatal("task-aligned thinking must be kept")
	}
	if !IsUnanchoredThinkingConfabulation("The user wants to modify a service worker.", "hey hey") {
		t.Fatal("task narrative on casual ping anchor must be dropped")
	}
	terraform := "The user is encountering an error in a Terraform AWS configuration.\n\n```\nError: Invalid Parameter Combination\n```\n\nLet me search for relevant files in the workspace."
	if !IsUnanchoredThinkingConfabulation(terraform, "buat web keren di /tmp/profile") {
		t.Fatal("terraform bleed must be dropped for unrelated web task")
	}
}

func TestIsModelResponseArtifactLargeDump(t *testing.T) {
	body := strings.Repeat("import { x } from 'y'\nexport function Foo() {}\n", 50)
	if !IsModelResponseArtifact(body) {
		t.Fatal("large multi-line code dump must be detected")
	}
}
