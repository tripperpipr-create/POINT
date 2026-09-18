package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"local-agent-workbench/internal/domain"
)

const (
	curatorDuplicateThreshold = 0.72
	curatorStaleUnusedAge     = 45 * 24 * time.Hour
	curatorStaleObservedAge   = 120 * 24 * time.Hour
)

// buildSkillCuration returns evidence-linked suggestions only. In particular,
// it never edits a Skill, binding, Blueprint, permission or tool policy.
func buildSkillCuration(
	skills []domain.SkillDefinition,
	agents []domain.ProjectAgent,
	blueprints []domain.AgentBlueprint,
	projectSkills []domain.ProjectSkillInstance,
	outcomes []domain.SkillOutcome,
	now time.Time,
) []domain.SkillCurationSuggestion {
	attached := curatorAttachedSkills(agents, blueprints, projectSkills)
	uses, latest := curatorSkillUsage(outcomes)
	result := make([]domain.SkillCurationSuggestion, 0)
	for left := 0; left < len(skills); left++ {
		if curatorDeprecated(skills[left]) {
			continue
		}
		for right := left + 1; right < len(skills); right++ {
			if curatorDeprecated(skills[right]) {
				continue
			}
			first, second := skills[left], skills[right]
			textSimilarity := curatorJaccard(curatorSkillTokens(first), curatorSkillTokens(second))
			toolSimilarity := curatorJaccard(curatorTokens(first.RequiredTools), curatorTokens(second.RequiredTools))
			sameName := curatorNormalizedName(first.Name) != "" && curatorNormalizedName(first.Name) == curatorNormalizedName(second.Name)
			duplicate := (textSimilarity >= curatorDuplicateThreshold && toolSimilarity >= 0.5) || (sameName && textSimilarity >= 0.5 && toolSimilarity == 1)
			if duplicate {
				primary, related := curatorPrimary(first, second, attached, uses)
				confidence := math.Min(0.99, textSimilarity*0.75+toolSimilarity*0.25)
				result = append(result, domain.SkillCurationSuggestion{
					ID: learningRecordID("curation", "duplicate", first.ID, second.ID), Kind: "duplicate", Action: "merge",
					PrimarySkillID: primary.ID, RelatedSkillIDs: []string{related.ID},
					Title: "Возможный дубликат Skills", Summary: fmt.Sprintf("Сведите «%s» и «%s» в одну проверяемую процедуру после ручного сравнения.", first.Name, second.Name),
					Evidence:   []string{fmt.Sprintf("сходство текста: %.0f%%", textSimilarity*100), fmt.Sprintf("сходство required tools: %.0f%%", toolSimilarity*100), fmt.Sprintf("наблюдений: %d и %d", uses[first.ID], uses[second.ID])},
					Confidence: confidence, GeneratedAt: now,
				})
				continue
			}
			if sameName && (textSimilarity < 0.5 || toolSimilarity < 0.5) {
				result = append(result, domain.SkillCurationSuggestion{
					ID: learningRecordID("curation", "conflict", first.ID, second.ID), Kind: "conflict", Action: "review",
					PrimarySkillID: first.ID, RelatedSkillIDs: []string{second.ID},
					Title: "Расходящиеся определения одного Skill", Summary: fmt.Sprintf("Два Skills называются «%s», но задают разные процедуры или наборы tools.", first.Name),
					Evidence:   []string{fmt.Sprintf("сходство текста: %.0f%%", textSimilarity*100), fmt.Sprintf("сходство required tools: %.0f%%", toolSimilarity*100), "автоматическое разрешение конфликта запрещено"},
					Confidence: math.Max(0.7, 1-(textSimilarity+toolSimilarity)/2), GeneratedAt: now,
				})
			}
		}
	}
	result = append(result, curatorRegressionSuggestions(skills, outcomes, now)...)
	for _, skill := range skills {
		if skill.ID == "" || curatorDeprecated(skill) || attached[skill.ID] {
			continue
		}
		last := latest[skill.ID]
		stale := uses[skill.ID] == 0 && !skill.CreatedAt.IsZero() && now.Sub(skill.CreatedAt) >= curatorStaleUnusedAge
		if uses[skill.ID] > 0 && !last.IsZero() && now.Sub(last) >= curatorStaleObservedAge {
			stale = true
		}
		if !stale {
			continue
		}
		evidence := []string{"не привязан к агентам, Blueprint или активному Project Skill", fmt.Sprintf("наблюдений в доступной истории: %d", uses[skill.ID])}
		if !last.IsZero() {
			evidence = append(evidence, "последнее наблюдение: "+last.Format("2006-01-02"))
		} else {
			evidence = append(evidence, "создан: "+skill.CreatedAt.Format("2006-01-02"))
		}
		result = append(result, domain.SkillCurationSuggestion{
			ID: learningRecordID("curation", "stale", skill.ID), Kind: "stale", Action: "deprecate",
			PrimarySkillID: skill.ID, Title: "Неиспользуемый Skill-кандидат", Summary: fmt.Sprintf("Проверьте «%s» и пометьте устаревшим, если он больше не нужен.", skill.Name),
			Evidence: evidence, Confidence: 0.8, GeneratedAt: now,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		priority := map[string]int{"conflict": 0, "regression": 1, "duplicate": 2, "stale": 3}
		if priority[result[i].Kind] != priority[result[j].Kind] {
			return priority[result[i].Kind] < priority[result[j].Kind]
		}
		if result[i].Confidence != result[j].Confidence {
			return result[i].Confidence > result[j].Confidence
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func curatorRegressionSuggestions(skills []domain.SkillDefinition, outcomes []domain.SkillOutcome, now time.Time) []domain.SkillCurationSuggestion {
	type regressionEvidence struct {
		skillID, name, digest string
		runs, adverse         int
		toolCalls, failures   int
		verificationGaps      int
		feedbackRuns          int
		revisionRuns          int
	}
	skillNames := map[string]string{}
	deprecated := map[string]bool{}
	for _, skill := range skills {
		skillNames[skill.ID] = skill.Name
		deprecated[skill.ID] = curatorDeprecated(skill)
	}
	buckets := map[string]*regressionEvidence{}
	seen := map[string]bool{}
	for _, outcome := range outcomes {
		if outcome.SkillID == "" || outcome.SkillDigest == "" || outcome.RunID == "" || deprecated[outcome.SkillID] {
			continue
		}
		key := outcome.SkillID + "\x00" + outcome.SkillDigest
		runKey := key + "\x00" + outcome.RunID
		if seen[runKey] {
			continue
		}
		seen[runKey] = true
		if buckets[key] == nil {
			name := outcome.SkillName
			if name == "" {
				name = skillNames[outcome.SkillID]
			}
			buckets[key] = &regressionEvidence{skillID: outcome.SkillID, name: name, digest: outcome.SkillDigest}
		}
		bucket := buckets[key]
		bucket.runs++
		bucket.toolCalls += outcome.ToolCalls
		bucket.failures += outcome.ToolFailures
		adverse := outcome.RunStatus != domain.RunCompleted || outcome.Health != "healthy" || outcome.ToolFailures > 0 || (outcome.VerificationRequired && !outcome.VerificationRecorded) || outcome.FeedbackCount > 0 || outcome.CompletionRevisions > 0
		if adverse {
			bucket.adverse++
		}
		if outcome.VerificationRequired && !outcome.VerificationRecorded {
			bucket.verificationGaps++
		}
		if outcome.FeedbackCount > 0 {
			bucket.feedbackRuns++
		}
		if outcome.CompletionRevisions > 0 {
			bucket.revisionRuns++
		}
	}
	result := make([]domain.SkillCurationSuggestion, 0)
	for _, bucket := range buckets {
		if bucket.runs < 3 || bucket.adverse < 2 || bucket.adverse*100 < bucket.runs*40 {
			continue
		}
		evidence := []string{
			fmt.Sprintf("неблагополучных Run: %d из %d", bucket.adverse, bucket.runs),
			fmt.Sprintf("tool failures: %d из %d вызовов", bucket.failures, bucket.toolCalls),
			fmt.Sprintf("пробелы верификации: %d · коррекции: %d · возвраты: %d", bucket.verificationGaps, bucket.feedbackRuns, bucket.revisionRuns),
			"точная версия: " + bucket.digest[:min(12, len(bucket.digest))],
		}
		result = append(result, domain.SkillCurationSuggestion{
			ID: learningRecordID("curation", "regression", bucket.skillID, bucket.digest), Kind: "regression", Action: "review",
			PrimarySkillID: bucket.skillID, Title: "Повторяемая деградация версии Skill",
			Summary:  fmt.Sprintf("Проверьте «%s»: негативный результат повторился на точной загруженной версии.", bucket.name),
			Evidence: evidence, Confidence: math.Min(0.98, float64(bucket.adverse)/float64(bucket.runs)), GeneratedAt: now,
		})
	}
	return result
}

func curatorAttachedSkills(agents []domain.ProjectAgent, blueprints []domain.AgentBlueprint, projectSkills []domain.ProjectSkillInstance) map[string]bool {
	result := map[string]bool{}
	for _, agent := range agents {
		for _, id := range agent.SkillIDs {
			result[id] = true
		}
	}
	for _, blueprint := range blueprints {
		for _, id := range blueprint.SkillIDs {
			result[id] = true
		}
	}
	for _, instance := range projectSkills {
		if instance.Enabled {
			result[instance.SkillID] = true
		}
	}
	return result
}

func curatorSkillUsage(outcomes []domain.SkillOutcome) (map[string]int, map[string]time.Time) {
	uses := map[string]int{}
	latest := map[string]time.Time{}
	seen := map[string]bool{}
	for _, outcome := range outcomes {
		key := outcome.SkillID + "\x00" + outcome.RunID
		if outcome.SkillID == "" || outcome.RunID == "" || seen[key] {
			continue
		}
		seen[key] = true
		uses[outcome.SkillID]++
		if outcome.CreatedAt.After(latest[outcome.SkillID]) {
			latest[outcome.SkillID] = outcome.CreatedAt
		}
	}
	return uses, latest
}

func curatorPrimary(first, second domain.SkillDefinition, attached map[string]bool, uses map[string]int) (domain.SkillDefinition, domain.SkillDefinition) {
	if attached[first.ID] != attached[second.ID] {
		if attached[second.ID] {
			return second, first
		}
		return first, second
	}
	if uses[first.ID] != uses[second.ID] {
		if uses[second.ID] > uses[first.ID] {
			return second, first
		}
		return first, second
	}
	if !first.CreatedAt.Equal(second.CreatedAt) && first.CreatedAt.After(second.CreatedAt) {
		return second, first
	}
	return first, second
}

func curatorDeprecated(skill domain.SkillDefinition) bool {
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(skill.Configuration["lifecycleStatus"])), "deprecated")
}

func curatorNormalizedName(value string) string {
	return strings.Join(curatorWords(value), " ")
}

func curatorSkillTokens(skill domain.SkillDefinition) map[string]bool {
	return curatorTokens(append(curatorWords(skill.Name+" "+skill.Description+" "+skill.Instructions), skill.RequiredTools...))
}

func curatorTokens(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		for _, token := range curatorWords(value) {
			result[token] = true
		}
	}
	return result
}

func curatorWords(value string) []string {
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "this": true, "that": true, "use": true, "using": true, "и": true, "в": true, "на": true, "для": true, "с": true, "по": true, "это": true, "как": true, "или": true}
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) >= 2 && !stop[field] {
			result = append(result, field)
		}
	}
	return result
}

func curatorJaccard(left, right map[string]bool) float64 {
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	intersection, union := 0, len(left)
	for token := range right {
		if left[token] {
			intersection++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
