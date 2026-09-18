// Навыки: сохранение, проверка требуемых инструментов, выдача агенту.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (a *App) SaveSkill(skill domain.SkillDefinition) (domain.SkillDefinition, error) {
	skill.Name = strings.TrimSpace(skill.Name)
	skill.Description = strings.TrimSpace(skill.Description)
	skill.Instructions = strings.TrimSpace(skill.Instructions)
	if skill.Name == "" || len([]rune(skill.Name)) > 120 {
		return domain.SkillDefinition{}, errors.New("skill name is required and must not exceed 120 characters")
	}
	if len([]rune(skill.Description)) > 4096 {
		return domain.SkillDefinition{}, errors.New("skill description exceeds 4096 characters")
	}
	if skill.Instructions == "" || len([]rune(skill.Instructions)) > 32768 {
		return domain.SkillDefinition{}, errors.New("skill instructions are required and must not exceed 32768 characters")
	}
	var err error
	if skill.References, err = normalizeSkillList("references", skill.References, 32, 4096); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.Scripts, err = normalizeSkillList("scripts", skill.Scripts, 32, 4096); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.RequiredTools, err = normalizeSkillList("required tools", skill.RequiredTools, 32, 200); err != nil {
		return domain.SkillDefinition{}, err
	}
	if err = a.validateSkillRequiredTools(skill.RequiredTools); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.PermissionDelta, err = normalizeSkillPermissionDelta(skill.PermissionDelta); err != nil {
		return domain.SkillDefinition{}, err
	}
	permissionKeys := make([]string, 0, len(skill.PermissionDelta))
	for key := range skill.PermissionDelta {
		if key != "network" && !strings.HasPrefix(strings.ToLower(key), "network:") {
			permissionKeys = append(permissionKeys, key)
		}
	}
	if err = a.validateSkillRequiredTools(permissionKeys); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.Configuration == nil {
		skill.Configuration = map[string]any{}
	}
	if encoded, encodeErr := json.Marshal(skill.Configuration); encodeErr != nil || len(encoded) > 64*1024 {
		return domain.SkillDefinition{}, errors.New("skill configuration must be valid JSON not exceeding 64 KiB")
	}
	existing, err := a.store.ListSkills(context.Background())
	if err != nil {
		return domain.SkillDefinition{}, err
	}
	var previous *domain.SkillDefinition
	for index := range existing {
		item := existing[index]
		if skill.ID != "" && item.ID == skill.ID {
			previous = &item
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Name), skill.Name) {
			return domain.SkillDefinition{}, fmt.Errorf("skill named %q already exists", skill.Name)
		}
	}
	now := time.Now().UTC()
	if skill.ID == "" {
		skill.ID = domain.NewID("skill")
		skill.CreatedAt = now
	} else if previous != nil {
		skill.CreatedAt = previous.CreatedAt
	}
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	if err := a.store.SaveSkill(context.Background(), skill); err != nil {
		return domain.SkillDefinition{}, err
	}
	return skill, nil
}

func (a *App) validateSkillRequiredTools(tools []string) error {
	known := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		known[item.Name] = true
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return err
	}
	for _, tool := range customTools {
		known[tool.ID] = true
	}
	for _, tool := range tools {
		if !known[tool] {
			return fmt.Errorf("skill references unknown tool %q", tool)
		}
	}
	return nil
}

func normalizeSkillPermissionDelta(values map[string]domain.ToolPolicy) (map[string]domain.ToolPolicy, error) {
	if len(values) == 0 {
		return map[string]domain.ToolPolicy{}, nil
	}
	if len(values) > 32 {
		return nil, errors.New("skill permission delta exceeds 32 entries")
	}
	result := make(map[string]domain.ToolPolicy, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = domain.ToolPolicy(strings.ToUpper(strings.TrimSpace(string(value))))
		if key == "" || len([]rune(key)) > 200 || (value != domain.ToolPolicyAllow && value != domain.ToolPolicyAsk && value != domain.ToolPolicyDeny) {
			return nil, errors.New("skill permission delta contains an invalid entry")
		}
		result[key] = value
	}
	return result, nil
}

func normalizeSkillList(label string, values []string, maxItems, maxRunes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("skill %s exceed %d entries", label, maxItems)
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > maxRunes {
			return nil, fmt.Errorf("skill %s entry exceeds %d characters", label, maxRunes)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func (a *App) EquipSkill(workspaceID, skillID string, configuration map[string]any) (domain.ProjectSkillInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	if workspaceID != "" && workspaceID != ws.ID {
		return domain.ProjectSkillInstance{}, errors.New("project skill belongs to another workspace")
	}
	workspaceID = ws.ID
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return domain.ProjectSkillInstance{}, errors.New("skill id is required")
	}
	skills, err := a.store.ListSkills(context.Background())
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	found := false
	for _, skill := range skills {
		if skill.ID == skillID {
			if curatorDeprecated(skill) {
				return domain.ProjectSkillInstance{}, fmt.Errorf("skill %q is deprecated and cannot be equipped", skill.Name)
			}
			found = true
			break
		}
	}
	if !found {
		return domain.ProjectSkillInstance{}, fmt.Errorf("skill %q not found", skillID)
	}
	existing, err := a.store.ListProjectSkills(context.Background(), workspaceID)
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	for _, instance := range existing {
		if instance.SkillID == skillID && instance.Enabled {
			return instance, nil
		}
	}
	now := time.Now().UTC()
	instance := domain.ProjectSkillInstance{
		ID: domain.NewID("projectskill"), WorkspaceID: workspaceID, SkillID: skillID,
		Configuration: configuration, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.SaveProjectSkill(context.Background(), instance); err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	return instance, nil
}

type SkillEquipPreview struct {
	Skill           domain.SkillDefinition       `json:"skill"`
	PermissionDelta map[string]domain.ToolPolicy `json:"permissionDelta"`
	RequiredTools   []string                     `json:"requiredTools,omitempty"`
	AlreadyEquipped bool                         `json:"alreadyEquipped"`
	Instance        *domain.ProjectSkillInstance `json:"instance,omitempty"`
}

func (a *App) PreviewSkillEquip(workspaceID, skillID string) (SkillEquipPreview, error) {
	if workspaceID == "" {
		ws, err := a.requireWorkspace()
		if err != nil {
			return SkillEquipPreview{}, err
		}
		workspaceID = ws.ID
	}
	skills, err := a.store.ListSkills(context.Background())
	if err != nil {
		return SkillEquipPreview{}, err
	}
	var skill *domain.SkillDefinition
	for index := range skills {
		if skills[index].ID == skillID {
			skill = &skills[index]
			break
		}
	}
	if skill == nil {
		return SkillEquipPreview{}, fmt.Errorf("skill %s not found", skillID)
	}
	if curatorDeprecated(*skill) {
		return SkillEquipPreview{}, fmt.Errorf("skill %q is deprecated and cannot be equipped", skill.Name)
	}
	preview := SkillEquipPreview{
		Skill: *skill, PermissionDelta: skill.PermissionDelta, RequiredTools: append([]string(nil), skill.RequiredTools...),
	}
	equipped, err := a.store.ListProjectSkills(context.Background(), workspaceID)
	if err != nil {
		return SkillEquipPreview{}, err
	}
	for index := range equipped {
		if equipped[index].SkillID == skillID && equipped[index].Enabled {
			preview.AlreadyEquipped = true
			instance := equipped[index]
			preview.Instance = &instance
			break
		}
	}
	return preview, nil
}
