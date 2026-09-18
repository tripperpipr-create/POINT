package app

import (
	"context"
	"fmt"
	"strings"
)

func (a *App) ForkMasterConversation(ctx context.Context, id, messageID string) (MasterChatView, error) {
	newID, err := a.store.ForkMasterConversation(ctx, a.currentWorldID(), id, messageID)
	if err != nil {
		return MasterChatView{}, err
	}
	return a.MasterSessionHistory(ctx, newID, false)
}
func (a *App) ExportMasterConversation(ctx context.Context, id string) (string, error) {
	_, sessions, err := a.sessionMasterService(ctx, a.masterChatService(ctx, a.masterProjectFacts(ctx)), id)
	if err != nil {
		return "", err
	}
	id = sessions.Active
	pages := []string{}
	var before int64
	for {
		p, err := a.store.MasterMessagePage(ctx, a.currentWorldID(), id, before, "", 200)
		if err != nil {
			return "", err
		}
		var part strings.Builder
		for _, m := range p.Items {
			role := "Мастер"
			if m.Role == "user" {
				role = "Вы"
			}
			fmt.Fprintf(&part, "## %s\n\n%s\n\n", role, m.Content)
			for _, att := range m.Attachments {
				if att.Kind == "image" {
					fmt.Fprintf(&part, "Вложение: %s (изображение)\n\n", att.Name)
				} else {
					fmt.Fprintf(&part, "Контекст: %s\n\n````\n%s\n````\n\n", att.Name, att.Content)
				}
			}
		}
		pages = append(pages, part.String())
		if !p.HasMore {
			break
		}
		before = p.Before
	}
	var out strings.Builder
	for _, v := range sessions.Items {
		if v.ID == id {
			fmt.Fprintf(&out, "# %s\n\n", v.Title)
		}
	}
	for i := len(pages) - 1; i >= 0; i-- {
		out.WriteString(pages[i])
	}
	return out.String(), nil
}
