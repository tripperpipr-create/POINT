package app

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
	"strings"
)

type MasterFilesView struct {
	WorkspaceID string            `json:"workspaceId"`
	Path        string            `json:"path"`
	Files       []domain.FileNode `json:"files"`
}

func (a *App) MasterFiles(ctx context.Context) (MasterFilesView, error) {
	ctx, err := a.WithMasterWorkspace(ctx, "")
	if err != nil {
		return MasterFilesView{}, err
	}
	scope := ctx.Value(masterScopeKey{}).(masterScope)
	files, err := scope.FS.List(ctx, 5)
	return MasterFilesView{scope.Workspace.ID, scope.Workspace.Path, files}, err
}

// Copy context into an explicitly selected project conversation. The source folder remains untouched.
func (a *App) ContinuePointChat(ctx context.Context, id, target string) (MasterChatView, error) {
	ctx, err := a.WithMasterWorkspace(ctx, "")
	if err != nil {
		return MasterChatView{}, err
	}
	source := a.masterWorldID(ctx)
	if !strings.HasPrefix(source, "point-chat-") || target == "" || target != a.currentWorldID() {
		return MasterChatView{}, errors.New("select and open the target project explicitly")
	}
	items, err := a.store.MasterConversations(ctx, source)
	if err != nil {
		return MasterChatView{}, err
	}
	var chat *MasterSession
	for i := range items {
		if items[i].ID == id {
			chat = &items[i]
		}
	}
	if chat == nil {
		return MasterChatView{}, errors.New("conversation not found")
	}
	copy := *chat
	copy.ID = domain.NewID("chat")
	copy.WorkspaceID = target
	copy.ScopeKind = "project"
	copy.WorkspacePath = ""
	copy.ParentID = chat.ID
	copy.Temporary = false
	copy.Archived = false
	copy.BranchOffer = ""
	copy.BranchName = ""
	copy.WorkMode = "auto"
	if err = a.store.SaveMasterConversation(ctx, copy); err != nil {
		return MasterChatView{}, err
	}
	if err = a.store.CopyMasterContext(ctx, source, target, id, copy.ID, ""); err != nil {
		return MasterChatView{}, err
	}
	if err = a.store.SaveSetting(ctx, "master.active."+target, copy.ID); err != nil {
		return MasterChatView{}, err
	}
	targetCtx, err := a.WithMasterWorkspace(ctx, target)
	if err != nil {
		return MasterChatView{}, err
	}
	return a.MasterSessionHistory(targetCtx, copy.ID, false)
}
