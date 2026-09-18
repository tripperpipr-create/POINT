package domain

import (
	"testing"
	"time"
)

func TestSkillDefinitionAttributionMatchesRuntimeAndIgnoresStorageTimestamps(t *testing.T) {
	skill := SkillDefinition{
		ID: "skill-review", Name: "Review", Description: " Inspect changes ", Instructions: "Verify the result.",
		RequiredTools: []string{"read_file"}, PermissionDelta: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
		Configuration: map[string]any{"revision": 4, "promotionStatus": "candidate"},
		CreatedAt:     time.Unix(1, 0), UpdatedAt: time.Unix(2, 0),
	}
	definition := SkillDefinitionAttribution(skill)
	runtime := SkillRuntimeAttribution(SkillRuntime{
		ID: skill.ID, Name: skill.Name, Description: "Inspect changes", Instructions: skill.Instructions,
		RequiredTools: []string{"read_file"}, PermissionRequirements: map[string]ToolPolicy{"read_file": ToolPolicyAllow}, Configuration: map[string]any{"revision": 4, "promotionStatus": "candidate"},
	})
	if definition != runtime || definition.Revision != 4 || len(definition.Digest) != 64 {
		t.Fatalf("definition=%#v runtime=%#v", definition, runtime)
	}
	skill.UpdatedAt = time.Unix(999, 0)
	if changed := SkillDefinitionAttribution(skill); changed.Digest != definition.Digest {
		t.Fatalf("storage timestamp changed runtime digest: before=%s after=%s", definition.Digest, changed.Digest)
	}
	skill.Instructions = "Verify tests and the result."
	if changed := SkillDefinitionAttribution(skill); changed.Digest == definition.Digest {
		t.Fatal("instruction change did not change runtime digest")
	}
}

func TestRunConfigurationSnapshotCarriesStableExactDigests(t *testing.T) {
	profile := AgentProfile{ID: "agent-1", Model: "model-a", AllowedTools: []string{"tool-a"}, EquippedSkills: []SkillRuntime{{
		ID: "skill-a", Name: "A", Instructions: "Do A", Configuration: map[string]any{"revision": 2},
	}}}
	captured := time.Unix(10, 0).UTC()
	first := NewRunConfigurationSnapshot("1.2.3", profile, []CustomTool{{ID: "tool-a", DisplayName: "A"}, {ID: "tool-b", DisplayName: "B"}}, captured)
	second := NewRunConfigurationSnapshot("1.2.3", profile, []CustomTool{{ID: "tool-a", DisplayName: "A"}, {ID: "tool-b", DisplayName: "B"}}, captured.Add(time.Hour))
	if first.SchemaVersion != 3 || first.ProfileDigest == "" || first.ConfigurationDigest == "" || first.EgressPolicyDigest == "" || first.EgressPolicyMode != "DENY" || first.ProfileDigest != second.ProfileDigest || first.ConfigurationDigest != second.ConfigurationDigest {
		t.Fatalf("digests are absent or time-dependent: first=%#v second=%#v", first, second)
	}
	if len(first.SkillAttributions) != 1 || first.SkillAttributions[0].Revision != 2 {
		t.Fatalf("skill attribution=%#v", first.SkillAttributions)
	}
	profile.Model = "model-b"
	changed := NewRunConfigurationSnapshot("1.2.3", profile, nil, captured)
	if changed.ProfileDigest == first.ProfileDigest || changed.ConfigurationDigest == first.ConfigurationDigest {
		t.Fatal("model change did not change exact configuration digests")
	}
	profile.ToolPolicies = map[string]string{"network": "ALLOWLIST", "network:registry.npmjs.org:443": "ALLOW"}
	allowlisted := NewRunConfigurationSnapshot("1.2.3", profile, nil, captured)
	profile.ToolPolicies = map[string]string{"network": "ALLOWLIST", "network:registry.npmjs.org:8443": "ALLOW"}
	changedDestination := NewRunConfigurationSnapshot("1.2.3", profile, nil, captured)
	if allowlisted.EgressPolicyMode != "ALLOWLIST" || allowlisted.EgressPolicyDigest == first.EgressPolicyDigest || allowlisted.EgressPolicyDigest == changedDestination.EgressPolicyDigest || allowlisted.ConfigurationDigest == changedDestination.ConfigurationDigest {
		t.Fatalf("egress policy was not independently attributed: allowlisted=%#v changed=%#v", allowlisted, changedDestination)
	}
}

func TestWithEffectiveModelUpdatesDigests(t *testing.T) {
	profile := DefaultProfile()
	profile.Model = "primary"
	snapshot := NewRunConfigurationSnapshot("1.2.3", profile, nil, time.Now().UTC())
	updated := snapshot.WithEffectiveModel("fallback")
	if updated.Profile.Model != "fallback" {
		t.Fatalf("model=%q", updated.Profile.Model)
	}
	if updated.ConfigurationDigest == snapshot.ConfigurationDigest || updated.ProfileDigest == snapshot.ProfileDigest {
		t.Fatal("effective model must change digests")
	}
	same := snapshot.WithEffectiveModel("primary")
	if same.ConfigurationDigest != snapshot.ConfigurationDigest {
		t.Fatal("identical model must keep digests")
	}
}

func TestManagedSkillPayloadDigestIgnoresRolloutMetadataOnly(t *testing.T) {
	skill := SkillDefinition{
		ID: "skill-canary", Name: "Canary", Instructions: "Verify the boundary.",
		Configuration: map[string]any{
			"managedBy": "agent-hub-self-improvement", "revision": 2,
			"promotionStatus": "candidate", "sourceRuns": []string{"run-a"}, "behaviorOption": "strict",
		},
	}
	before := SkillDefinitionAttribution(skill)
	skill.Configuration["promotionStatus"] = "promoted"
	skill.Configuration["sourceRuns"] = []string{"run-a", "run-b"}
	skill.Configuration["promotionWorkspaceCount"] = 2
	afterPromotion := SkillDefinitionAttribution(skill)
	if afterPromotion.Digest != before.Digest || afterPromotion.Revision != before.Revision {
		t.Fatalf("rollout metadata changed payload identity: before=%#v after=%#v", before, afterPromotion)
	}
	skill.Configuration["behaviorOption"] = "relaxed"
	if changed := SkillDefinitionAttribution(skill); changed.Digest == before.Digest {
		t.Fatal("behavior-affecting runtime configuration did not change payload digest")
	}
}
