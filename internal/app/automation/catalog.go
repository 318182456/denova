package automationapp

import (
	"fmt"
	"log/slog"

	"denova/internal/automation"
)

// ProjectCatalog is a read-only projection of one registered Project. Failures
// stay local to that Project; no catalog data becomes a second recovery source.
type ProjectCatalog struct {
	ProjectID string                        `json:"project_id"`
	Tasks     []automation.Task             `json:"tasks"`
	Inbox     []automation.TriggerInboxItem `json:"inbox"`
	Error     string                        `json:"error,omitempty"`
}

// CatalogSnapshot reads each Project Store once instead of repeatedly scanning
// the whole registry for every Project's tasks and inbox. Execution and mutation
// remain scoped to the owning Project Agent.
func (s *Service) CatalogSnapshot() ([]ProjectCatalog, error) {
	catalog, err := s.host.Catalog()
	if err != nil {
		return nil, err
	}
	groups := make([]ProjectCatalog, 0, len(catalog.Projects))
	for _, location := range catalog.Projects {
		group := ProjectCatalog{ProjectID: location.ProjectID, Tasks: []automation.Task{}, Inbox: []automation.TriggerInboxItem{}}
		store := automation.NewProjectStore(catalog.DataDir, location.ProjectID, location.Workspace, location.StateRoot)
		group.Tasks, err = s.projectTasks(store.ListForTarget(automation.ExecutionTarget{
			Kind: automation.TargetKindWorkspace, ProjectID: location.ProjectID, Workspace: location.Workspace,
		}))
		if err == nil {
			group.Inbox, err = store.ListInbox()
		}
		if err != nil {
			group.Tasks, group.Inbox = []automation.Task{}, []automation.TriggerInboxItem{}
			group.Error = fmt.Sprintf("read Project automation catalog: %v", err)
			slog.Error("[automation] failed to read Project catalog", "project_id", location.ProjectID, "err", err)
		}
		groups = append(groups, group)
	}
	return groups, nil
}
