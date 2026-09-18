package companion

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// InterveneContext carries live IDE focus for rare-speak gating.
type InterveneContext struct {
	FocusPath string
	Now       time.Time
}

// ShouldIntervene decides whether a candidate IDE/hub signal is worth surfacing.
// Companion observes silently always; speaking is reserved for valuable nudges.
func ShouldIntervene(cfg domain.CompanionConfig, item domain.CompanionIntervention, observations []domain.IDEObservation, ctx InterveneContext) bool {
	if item.ID == "" {
		return false
	}
	initiative := cfg.Initiative
	if initiative <= 0 {
		initiative = 50
	}
	strictness := cfg.QuestionStrictness
	if strictness <= 0 {
		strictness = 70
	}
	now := ctx.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	level := strings.ToLower(strings.TrimSpace(item.Level))
	switch level {
	case "critical":
		// Critical almost always speaks, unless the underlying signal is stale noise.
		if isStaleIDESignal(item, observations, now, 45*time.Minute) && initiative < 40 {
			return false
		}
		return true
	case "warning":
		if initiative < 35 {
			return false
		}
		if strictness >= 85 && !ideSignalMatchesFocus(item, observations, ctx.FocusPath) {
			return false
		}
		if isStaleIDESignal(item, observations, now, 20*time.Minute) {
			return false
		}
		return true
	case "suggestion":
		if initiative < 70 {
			return false
		}
		if strictness >= 75 {
			return false
		}
		if isStaleIDESignal(item, observations, now, 15*time.Minute) {
			return false
		}
		return true
	default:
		return initiative >= 60
	}
}

func GateIDEInterventions(cfg domain.CompanionConfig, items []domain.CompanionIntervention, observations []domain.IDEObservation, ctx InterveneContext) []domain.CompanionIntervention {
	if len(items) == 0 {
		return items
	}
	initiative := cfg.Initiative
	if initiative <= 0 {
		initiative = 50
	}
	limit := 4
	switch {
	case initiative <= 30:
		limit = 1
	case initiative <= 50:
		limit = 2
	case initiative <= 70:
		limit = 3
	}
	out := make([]domain.CompanionIntervention, 0, min(limit, len(items)))
	for _, item := range items {
		if !ShouldIntervene(cfg, item, observations, ctx) {
			continue
		}
		// Prefer focus-matched signals when initiative is moderate.
		if initiative <= 55 && ctx.FocusPath != "" && strings.HasPrefix(item.ID, "ide-") {
			if !ideSignalMatchesFocus(item, observations, ctx.FocusPath) && item.Level != "critical" {
				continue
			}
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func isStaleIDESignal(item domain.CompanionIntervention, observations []domain.IDEObservation, now time.Time, maxAge time.Duration) bool {
	if !strings.HasPrefix(item.ID, "ide-") {
		return false
	}
	newest := time.Time{}
	for _, obs := range observations {
		if !observationBelongsToIntervention(item, obs) {
			continue
		}
		seen := obs.LastSeen
		if seen.IsZero() {
			seen = obs.ObservedAt
		}
		if seen.After(newest) {
			newest = seen
		}
	}
	if newest.IsZero() {
		return false
	}
	return now.Sub(newest) > maxAge
}

func ideSignalMatchesFocus(item domain.CompanionIntervention, observations []domain.IDEObservation, focusPath string) bool {
	focusPath = strings.ReplaceAll(strings.TrimSpace(focusPath), "\\", "/")
	if focusPath == "" {
		return true
	}
	if item.RelatedPath != "" {
		rel := strings.ReplaceAll(item.RelatedPath, "\\", "/")
		if strings.EqualFold(rel, focusPath) || strings.HasSuffix(focusPath, "/"+rel) || strings.HasSuffix(rel, "/"+focusPath) {
			return true
		}
	}
	for _, obs := range observations {
		if !observationBelongsToIntervention(item, obs) {
			continue
		}
		path := strings.ReplaceAll(obs.Path, "\\", "/")
		if path != "" && (strings.EqualFold(path, focusPath) || strings.HasSuffix(focusPath, "/"+path) || strings.HasSuffix(path, "/"+focusPath)) {
			return true
		}
		// SCM must not match solely via stamped batch FocusPath — that is editor focus, not a changed file.
		if item.ID == "ide-scm-dirty" {
			continue
		}
		focus := strings.ReplaceAll(obs.FocusPath, "\\", "/")
		if focus != "" && strings.EqualFold(focus, focusPath) {
			return true
		}
	}
	return false
}

func observationBelongsToIntervention(item domain.CompanionIntervention, obs domain.IDEObservation) bool {
	switch {
	case item.ID == "ide-diagnostics":
		return obs.Kind == "diagnostic" && (obs.Level == "error" || obs.Level == "warning")
	case strings.HasPrefix(item.ID, "ide-command-failed-"):
		return (obs.Kind == "terminal" || obs.Kind == "task") && obs.ID != "" && (item.RelatedID == obs.ID || strings.HasSuffix(item.ID, obs.ID))
	case item.ID == "ide-debug-active":
		return obs.Kind == "debug"
	case item.ID == "ide-run-active":
		return obs.Kind == "run"
	case item.ID == "ide-scm-dirty":
		return obs.Kind == "scm"
	default:
		return item.RelatedID != "" && item.RelatedID == obs.ID
	}
}

func ObservationNoveltyHash(item domain.IDEObservation) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		item.Kind, item.Level, item.Path, fmt.Sprintf("%d", item.Line), item.Summary, item.Command,
	}, "\x00")))
	return fmt.Sprintf("%x", digest[:8])
}

// InterventionOccurrenceKey builds a sticky speak/dismiss fingerprint.
// IDE signals key off observation NoveltyHash so Detail wording thrash does not reset cooldown.
func InterventionOccurrenceKey(item domain.CompanionIntervention, observations []domain.IDEObservation) string {
	if item.OccurrenceKey != "" {
		return item.OccurrenceKey
	}
	// Aggregate counters change their Detail on every sample (3/7 becomes
	// 3/8). Treat the active rate warning as one incident so dismissing it does
	// not create an immediately returning "new" notification. Dismissal cleanup
	// removes this key once the incident resolves, allowing a later incident to
	// surface normally.
	if item.ID == "usage-failure-rate" {
		digest := sha256.Sum256([]byte(strings.Join([]string{item.ID, item.Level}, "\x00")))
		return fmt.Sprintf("%x", digest[:8])
	}
	if strings.HasPrefix(item.ID, "ide-") {
		parts := []string{item.ID, strings.ToLower(strings.TrimSpace(item.Level))}
		novelty := make([]string, 0, 4)
		for _, obs := range observations {
			if !observationBelongsToIntervention(item, obs) {
				continue
			}
			hash := strings.TrimSpace(obs.NoveltyHash)
			if hash == "" {
				hash = ObservationNoveltyHash(obs)
			}
			novelty = append(novelty, hash)
		}
		sort.Strings(novelty)
		parts = append(parts, novelty...)
		digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
		return fmt.Sprintf("%x", digest[:8])
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		item.ID, item.Level, item.Title, item.Detail, item.ActionTab, item.RelatedID, item.RelatedPath,
		string(item.ActionKind), item.ActionLabel, item.ActionMessage,
	}, "\x00")))
	return fmt.Sprintf("%x", digest[:8])
}

// SpeakRecord remembers the last time Companion surfaced a given signal.
type SpeakRecord struct {
	OccurrenceKey string    `json:"occurrenceKey"`
	SpokenAt      time.Time `json:"spokenAt"`
	Level         string    `json:"level,omitempty"`
}

// SpeakMemory is workspace-scoped cooldown state for rare nudges.
type SpeakMemory struct {
	ByID map[string]SpeakRecord `json:"byId"`
}

func InitiativeVisibleLimit(cfg domain.CompanionConfig) int {
	initiative := cfg.Initiative
	if initiative <= 0 {
		initiative = 50
	}
	switch {
	case initiative <= 30:
		return 2
	case initiative <= 50:
		return 4
	case initiative <= 70:
		return 6
	default:
		return 8
	}
}

func speakCooldown(level string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "critical":
		return 8 * time.Minute
	case "warning":
		return 20 * time.Minute
	default:
		return 35 * time.Minute
	}
}

// ApplySpeakCooldown hides repeated soft nudges until cooldown expires or the occurrence changes.
// Critical signals stay visible while unresolved. Does not re-apply initiative scoring.
func ApplySpeakCooldown(cfg domain.CompanionConfig, items []domain.CompanionIntervention, observations []domain.IDEObservation, ctx InterveneContext, memory SpeakMemory) ([]domain.CompanionIntervention, SpeakMemory) {
	now := ctx.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if memory.ByID == nil {
		memory.ByID = map[string]SpeakRecord{}
	}
	out := make([]domain.CompanionIntervention, 0, len(items))
	next := SpeakMemory{ByID: map[string]SpeakRecord{}}
	for id, rec := range memory.ByID {
		next.ByID[id] = rec
	}
	for _, item := range items {
		item.OccurrenceKey = InterventionOccurrenceKey(item, observations)
		if !passesMergedSpeakGate(cfg, item, observations, ctx) {
			continue
		}
		rec, seen := memory.ByID[item.ID]
		if seen && rec.OccurrenceKey == item.OccurrenceKey && !rec.SpokenAt.IsZero() {
			if now.Sub(rec.SpokenAt) < speakCooldown(item.Level) {
				// Soft nudges (IDE + Hub/run) cool down; critical stays visible while active.
				if !strings.EqualFold(item.Level, "critical") {
					continue
				}
				out = append(out, item)
				next.ByID[item.ID] = SpeakRecord{OccurrenceKey: rec.OccurrenceKey, SpokenAt: rec.SpokenAt, Level: item.Level}
				continue
			}
		}
		out = append(out, item)
		next.ByID[item.ID] = SpeakRecord{OccurrenceKey: item.OccurrenceKey, SpokenAt: now, Level: item.Level}
	}
	// Keep speak stamps across transient clears for up to 2× cooldown; prune by age, not immediate inactivity.
	active := map[string]bool{}
	for _, item := range items {
		active[item.ID] = true
	}
	for id, rec := range next.ByID {
		if active[id] {
			continue
		}
		level := rec.Level
		if level == "" {
			level = "suggestion"
		}
		keepFor := speakCooldown(level) * 2
		if rec.SpokenAt.IsZero() || now.Sub(rec.SpokenAt) > keepFor {
			delete(next.ByID, id)
		}
	}
	return out, next
}

func passesMergedSpeakGate(cfg domain.CompanionConfig, item domain.CompanionIntervention, observations []domain.IDEObservation, ctx InterveneContext) bool {
	_ = observations
	_ = ctx
	// IDE items are already filtered by GateIDEInterventions.
	// Hub/run keep softGateNonIDE (suggestions only at quiet initiative ≤30);
	// soft speak cooldown below handles repeat warnings across IDE + Hub.
	return softGateNonIDE(cfg, item)
}

func softGateNonIDE(cfg domain.CompanionConfig, item domain.CompanionIntervention) bool {
	if strings.HasPrefix(item.ID, "ide-") {
		return true
	}
	initiative := cfg.Initiative
	if initiative <= 0 {
		initiative = 50
	}
	// Quiet companion hides non-IDE suggestion clutter; warnings still need ShouldIntervene.
	if strings.EqualFold(item.Level, "suggestion") && initiative <= 30 {
		return false
	}
	return true
}

// GateMergedInterventions applies initiative/speak gates to the full merged intervention set.
func GateMergedInterventions(cfg domain.CompanionConfig, items []domain.CompanionIntervention, observations []domain.IDEObservation, ctx InterveneContext, memory SpeakMemory) ([]domain.CompanionIntervention, SpeakMemory) {
	cooled, next := ApplySpeakCooldown(cfg, items, observations, ctx, memory)
	limit := InitiativeVisibleLimit(cfg)
	if len(cooled) <= limit {
		return cooled, next
	}
	return cooled[:limit], next
}

// LatestObservationFocus returns the newest non-empty focusPath from IDE observations.
func LatestObservationFocus(observations []domain.IDEObservation) string {
	newest := time.Time{}
	focus := ""
	for _, obs := range observations {
		path := strings.TrimSpace(obs.FocusPath)
		if path == "" {
			continue
		}
		seen := obs.LastSeen
		if seen.IsZero() {
			seen = obs.ObservedAt
		}
		if seen.After(newest) || focus == "" {
			newest = seen
			focus = path
		}
	}
	return focus
}
