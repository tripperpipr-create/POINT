package domain

import (
	"reflect"
	"time"
)

type WorkOrderRevisionDiff struct {
	ID               string    `json:"id"`
	WorkOrderID      string    `json:"workOrderId"`
	FromVersion      int       `json:"fromVersion"`
	ToVersion        int       `json:"toVersion"`
	FromDigest       string    `json:"fromDigest"`
	ToDigest         string    `json:"toDigest"`
	ChangedFields    []string  `json:"changedFields"`
	RequiresApproval bool      `json:"requiresApproval"`
	CreatedAt        time.Time `json:"createdAt"`
}

func DiffWorkOrders(before, after WorkOrder) WorkOrderRevisionDiff {
	changed := make([]string, 0, 12)
	add := func(name string, different bool) {
		if different {
			changed = append(changed, name)
		}
	}
	add("goal", before.Goal != after.Goal)
	add("scope", !reflect.DeepEqual(before.Scope, after.Scope))
	add("outOfScope", !reflect.DeepEqual(before.OutOfScope, after.OutOfScope))
	add("assumptions", !reflect.DeepEqual(before.Assumptions, after.Assumptions))
	add("sources", !reflect.DeepEqual(before.Sources, after.Sources))
	add("criteria", !reflect.DeepEqual(before.Criteria, after.Criteria))
	add("workspace", !reflect.DeepEqual(before.Workspace, after.Workspace))
	add("stack", !reflect.DeepEqual(before.Stack, after.Stack))
	add("roster", !reflect.DeepEqual(before.Roster, after.Roster))
	add("routing", !reflect.DeepEqual(before.Routing, after.Routing))
	add("network", !reflect.DeepEqual(before.Network, after.Network))
	add("secrets", !reflect.DeepEqual(before.Secrets, after.Secrets))
	add("budget", !reflect.DeepEqual(before.Budget, after.Budget))
	add("delivery", !reflect.DeepEqual(before.Delivery, after.Delivery))
	return WorkOrderRevisionDiff{
		ID: NewID("workorderdiff"), WorkOrderID: before.ID, FromVersion: before.Version, ToVersion: after.Version,
		FromDigest: WorkOrderDigest(before), ToDigest: WorkOrderDigest(after), ChangedFields: changed,
		RequiresApproval: len(changed) > 0, CreatedAt: time.Now().UTC(),
	}
}
