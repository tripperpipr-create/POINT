package storage

import (
	"context"
	"database/sql"
	"fmt"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveTeam(ctx context.Context, team domain.Team) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO teams(id,workspace_id,name,description,agent_ids,created_at,updated_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, agent_ids=excluded.agent_ids, updated_at=excluded.updated_at`,
		team.ID, team.WorkspaceID, team.Name, team.Description, marshalJSON(team.AgentIDs), formatTime(team.CreatedAt), formatTime(team.UpdatedAt))
	return err
}

func (s *SQLite) ListTeams(ctx context.Context, workspaceID string) ([]domain.Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,name,description,agent_ids,created_at,updated_at FROM teams WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Team
	for rows.Next() {
		var team domain.Team
		var agents, created, updated string
		if err = rows.Scan(&team.ID, &team.WorkspaceID, &team.Name, &team.Description, &agents, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(agents, &team.AgentIDs)
		team.CreatedAt, team.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, team)
	}
	return result, rows.Err()
}

// DeleteTeam распускает отряд проекта.
//
// Отряд под квест ядро собирает само («Отряд · <название квеста>»), но живёт он
// отдельно от квеста: закрытый и даже удалённый квест отряд за собой не уносит.
// Без маршрута удаления такие отряды копились, и каждый навсегда держал своих
// участников — роспуск персонажа отказывал, ссылаясь на отряд, которого человек
// никак не мог убрать.
//
// Условие на workspace_id держит удаление в границах открытого проекта.
func (s *SQLite) DeleteTeam(ctx context.Context, workspaceID, teamID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id=? AND workspace_id=?`, teamID, workspaceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("team %q not found in the open project", teamID)
	}
	return nil
}

func (s *SQLite) SaveQuest(ctx context.Context, quest domain.Quest) error {
	briefJSON, err := taskBriefJSON(quest.Brief)
	if err != nil {
		return err
	}
	var finished any
	if quest.FinishedAt != nil {
		finished = formatTime(*quest.FinishedAt)
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO quests(id,workspace_id,parent_id,title,description,objectives,constraints_json,definition_of_done,importance,status,team_id,flow_id,flow_run_id,flow_node_id,assigned_agent_id,budget_tokens,budget_cents,created_at,updated_at,finished_at,brief_json,kind,controller_state,controller_json,prerequisite_ids_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, title=excluded.title, description=excluded.description,
  objectives=excluded.objectives, constraints_json=excluded.constraints_json, definition_of_done=excluded.definition_of_done,
  importance=excluded.importance, status=excluded.status, team_id=excluded.team_id, flow_id=excluded.flow_id,
  flow_run_id=excluded.flow_run_id, flow_node_id=excluded.flow_node_id, assigned_agent_id=excluded.assigned_agent_id,
  budget_tokens=excluded.budget_tokens, budget_cents=excluded.budget_cents, updated_at=excluded.updated_at, finished_at=excluded.finished_at, brief_json=COALESCE(excluded.brief_json,quests.brief_json),
  kind=excluded.kind, controller_state=excluded.controller_state, controller_json=excluded.controller_json, prerequisite_ids_json=excluded.prerequisite_ids_json
WHERE quests.workspace_id=excluded.workspace_id`,
		quest.ID, quest.WorkspaceID, quest.ParentID, quest.Title, quest.Description, marshalJSON(quest.Objectives),
		marshalJSON(quest.Constraints), marshalJSON(quest.DefinitionOfDone), quest.Importance, quest.Status, quest.TeamID,
		quest.FlowID, quest.FlowRunID, quest.FlowNodeID, quest.AssignedAgentID, quest.BudgetTokens, quest.BudgetCents,
		formatTime(quest.CreatedAt), formatTime(quest.UpdatedAt), finished, briefJSON, quest.Kind, quest.ControllerState,
		marshalJSON(quest.Controller), marshalJSON(quest.PrerequisiteIDs))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("quest %q belongs to another workspace", quest.ID)
	}
	return nil
}

func (s *SQLite) DeleteUnstartedQuest(ctx context.Context, workspaceID, questID string) error {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM quests
WHERE id=? AND workspace_id=? AND status='draft' AND flow_id=''
  AND NOT EXISTS (SELECT 1 FROM quests child WHERE child.parent_id=quests.id)
  AND NOT EXISTS (SELECT 1 FROM executions execution WHERE execution.quest_id=quests.id)`,
		questID, workspaceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("quest %q is no longer an unstarted draft", questID)
	}
	return nil
}

func (s *SQLite) ListQuests(ctx context.Context, workspaceID string) ([]domain.Quest, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,parent_id,title,description,objectives,constraints_json,definition_of_done,importance,status,team_id,flow_id,flow_run_id,flow_node_id,assigned_agent_id,budget_tokens,budget_cents,created_at,updated_at,finished_at,brief_json,kind,controller_state,controller_json,prerequisite_ids_json
FROM quests WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Quest
	for rows.Next() {
		var quest domain.Quest
		var objectives, constraints, dod, created, updated, controller, prerequisites string
		var finished, briefJSON sql.NullString
		if err = rows.Scan(&quest.ID, &quest.WorkspaceID, &quest.ParentID, &quest.Title, &quest.Description, &objectives,
			&constraints, &dod, &quest.Importance, &quest.Status, &quest.TeamID, &quest.FlowID, &quest.FlowRunID,
			&quest.FlowNodeID, &quest.AssignedAgentID, &quest.BudgetTokens,
			&quest.BudgetCents, &created, &updated, &finished, &briefJSON, &quest.Kind, &quest.ControllerState, &controller, &prerequisites); err != nil {
			return nil, err
		}
		quest.Brief, err = decodeTaskBrief(briefJSON)
		if err != nil {
			return nil, err
		}
		unmarshalJSON(objectives, &quest.Objectives)
		unmarshalJSON(constraints, &quest.Constraints)
		unmarshalJSON(dod, &quest.DefinitionOfDone)
		unmarshalJSON(controller, &quest.Controller)
		unmarshalJSON(prerequisites, &quest.PrerequisiteIDs)
		quest.CreatedAt, quest.UpdatedAt = parseTime(created), parseTime(updated)
		if finished.Valid && finished.String != "" {
			t := parseTime(finished.String)
			quest.FinishedAt = &t
		}
		result = append(result, quest)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveFlow(ctx context.Context, flow domain.FlowGraph) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO flows(id,workspace_id,name,description,nodes,edges,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, nodes=excluded.nodes, edges=excluded.edges, updated_at=excluded.updated_at`,
		flow.ID, flow.WorkspaceID, flow.Name, flow.Description, marshalJSON(flow.Nodes), marshalJSON(flow.Edges),
		formatTime(flow.CreatedAt), formatTime(flow.UpdatedAt))
	return err
}

func (s *SQLite) ListFlows(ctx context.Context, workspaceID string) ([]domain.FlowGraph, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,name,description,nodes,edges,created_at,updated_at FROM flows WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.FlowGraph
	for rows.Next() {
		var flow domain.FlowGraph
		var nodes, edges, created, updated string
		if err = rows.Scan(&flow.ID, &flow.WorkspaceID, &flow.Name, &flow.Description, &nodes, &edges, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(nodes, &flow.Nodes)
		unmarshalJSON(edges, &flow.Edges)
		flow.CreatedAt, flow.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, flow)
	}
	return result, rows.Err()
}

func (s *SQLite) GetFlow(ctx context.Context, id string) (domain.FlowGraph, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,name,description,nodes,edges,created_at,updated_at FROM flows WHERE id=?`, id)
	var flow domain.FlowGraph
	var nodes, edges, created, updated string
	if err := row.Scan(&flow.ID, &flow.WorkspaceID, &flow.Name, &flow.Description, &nodes, &edges, &created, &updated); err != nil {
		return domain.FlowGraph{}, err
	}
	unmarshalJSON(nodes, &flow.Nodes)
	unmarshalJSON(edges, &flow.Edges)
	flow.CreatedAt, flow.UpdatedAt = parseTime(created), parseTime(updated)
	return flow, nil
}

// DeleteFlow убирает схему проекта.
//
// Хроника её запусков остаётся, как и у квеста: flow_runs — доказательство уже
// сделанной работы, и стирать её ради уборки списка нельзя. Внешнего ключа на
// flows у неё нет, поэтому осиротевший запуск читается сам по себе.
//
// Условие на workspace_id держит удаление в границах открытого проекта.
func (s *SQLite) DeleteFlow(ctx context.Context, workspaceID, flowID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM flows WHERE id=? AND workspace_id=?`, flowID, workspaceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("flow %q not found in the open project", flowID)
	}
	return nil
}

// DeleteQuest убирает квест проекта вместе со следами, которые без него теряют
// смысл: ссылками, перепланировками и выданными арендами инструментов.
//
// Хроника запусков, улики и учёт расходов остаются на месте. Это доказательства
// уже сделанной работы: удалять их вместе с карточкой квеста — значит стирать
// историю проекта ради уборки списка. Интерфейс показывает осиротевшие прогоны
// отдельным разделом «запуски без квеста», так что ссылка в пустоту не остаётся.
//
// Условие на workspace_id держит удаление в границах открытого проекта: иначе
// один и тот же идентификатор, пришедший из чужого окна, вычистил бы карточку
// соседнего проекта.
func (s *SQLite) DeleteQuest(ctx context.Context, workspaceID, questID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM quests WHERE id=? AND workspace_id=?`, questID, workspaceID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		_ = tx.Rollback()
		return fmt.Errorf("quest %q not found in the open project", questID)
	}
	for _, statement := range []string{
		`DELETE FROM quest_links WHERE quest_id=?`,
		`DELETE FROM quest_replans WHERE quest_id=?`,
		`DELETE FROM quest_tool_leases WHERE quest_id=?`,
	} {
		if _, err = tx.ExecContext(ctx, statement, questID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
