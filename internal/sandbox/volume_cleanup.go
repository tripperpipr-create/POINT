package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (b *ContainerBackend) ownedResource(ctx context.Context, kind, name, sandboxID, resource string) (bool, error) {
	labels := ".Labels"
	if kind == "container" {
		labels = ".Config.Labels"
	}
	format := fmt.Sprintf(`{{index %s "point.owner"}}|{{index %s "point.sandbox"}}|{{index %s "point.resource"}}`, labels, labels, labels)
	value, err := b.output(ctx, kind, "inspect", name, "--format", format)
	if missingDockerResource(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(value) != ownerIdentity(b.Root)+"|"+sandboxID+"|"+resource {
		return false, fmt.Errorf("refusing to remove foreign Docker %s %s: ownership labels differ", kind, name)
	}
	return true, nil
}

func (b *ContainerBackend) removeWorkspaceResources(ctx context.Context, r domain.SandboxRecord) error {
	s, loadErr := b.stateFor(r.Path)
	if loadErr == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.Watcher != nil {
			s.Watcher.Close()
		}
		if s.Gateway != nil {
			b.removeWarmGateway(ctx, s.Gateway)
			s.Gateway = nil
		}
	}
	if r.StorageMode != "volume" {
		b.workspaceStates.Delete(r.Path)
		return nil
	}
	for _, name := range []string{"point-stage-" + r.ID, "point-gateway-" + r.ID} {
		resource := "stage"
		if strings.HasPrefix(name, "point-gateway-") {
			resource = "gateway"
		}
		exists, err := b.ownedResource(ctx, "container", name, r.ID, resource)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := b.runInfrastructure(ctx, "rm", "--force", name); err != nil {
			return err
		}
	}
	network := "point-net-" + r.ID
	if exists, err := b.ownedResource(ctx, "network", network, r.ID, "network"); err != nil {
		return err
	} else if exists {
		if err = b.runInfrastructure(ctx, "network", "rm", network); err != nil {
			return err
		}
	}
	volume := r.WorkspaceVolume
	if volume == "" {
		volume = "point-work-" + r.ID
	}
	if exists, err := b.ownedResource(ctx, "volume", volume, r.ID, "workspace"); err != nil {
		return err
	} else if exists {
		if err = b.runInfrastructure(ctx, "volume", "rm", volume); err != nil {
			return err
		}
	}
	b.workspaceStates.Delete(r.Path)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return loadErr
	}
	b.releaseRuntimeExecution(r.Path)
	return nil
}

func missingDockerResource(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such volume") || strings.Contains(message, "no such container") || strings.Contains(message, "no such network") || strings.Contains(message, "not found") && !strings.Contains(message, "executable file")
}

// StopWorkspace releases execution resources while retaining the independent volume for a successor.
func (b *ContainerBackend) StopWorkspace(ctx context.Context, r domain.SandboxRecord) error {
	if r.StorageMode != "volume" {
		return nil
	}
	s, err := b.stateFor(r.Path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = b.stopWarmWorkspace(ctx, s); err != nil {
		return err
	}
	b.releaseRuntimeExecution(r.Path)
	return nil
}

// Called under s.mu. Benchmark ablations retain the active execution lease.
func (b *ContainerBackend) stopWarmWorkspace(ctx context.Context, s *volumeState) error {
	r := s.Record
	var err error
	if s.Container != "" {
		exists, ownershipErr := b.ownedResource(ctx, "container", s.Container, r.ID, "stage")
		if ownershipErr != nil {
			return ownershipErr
		}
		if exists {
			err = b.runInfrastructure(ctx, "rm", "--force", s.Container)
		}
		if err != nil && !missingDockerResource(err) {
			return err
		}
		s.Container = ""
	}
	if err = b.removeWarmGateway(ctx, s.Gateway); err != nil {
		return err
	}
	s.Gateway = nil
	return nil
}

// CleanupOrphans only removes labeled resources whose sandbox is absent from the durable database.
// A failed database read must never be interpreted as an empty live set.
func (b *ContainerBackend) CleanupOrphans(ctx context.Context, live []domain.SandboxRecord) error {
	keep := map[string]bool{}
	helpers := map[string]bool{}
	for _, r := range live {
		if r.ClosedAt == nil {
			keep[r.ID] = true
			if len(r.SandboxdDigest) == 71 {
				helpers["point-tools-"+ownerIdentity(b.Root)+"-"+r.SandboxdDigest[7:31]] = true
			}
		}
	}
	owner := "point.owner=" + ownerIdentity(b.Root)
	var errs []error
	for _, kind := range []string{"container", "network", "volume"} {
		format := "{{.ID}}"
		if kind == "volume" {
			format = "{{.Name}}"
		}
		out, err := b.output(ctx, kind, "ls", "--filter", "label="+owner, "--format", format)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, id := range strings.Fields(out) {
			label, err := b.output(ctx, kind, "inspect", id, "--format", `{{index .Labels "point.sandbox"}}`)
			if kind == "container" {
				label, err = b.output(ctx, kind, "inspect", id, "--format", `{{index .Config.Labels "point.sandbox"}}`)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			label = strings.TrimSpace(label)
			if label == "" || keep[label] {
				continue
			}
			created, err := b.output(ctx, kind, "inspect", id, "--format", `{{index .Labels "point.created"}}`)
			if kind == "container" {
				created, err = b.output(ctx, kind, "inspect", id, "--format", `{{index .Config.Labels "point.created"}}`)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(created))
			if err != nil || time.Since(at) < time.Hour {
				continue
			}
			resource, err := b.output(ctx, kind, "inspect", id, "--format", `{{index .Labels "point.resource"}}`)
			if kind == "container" {
				resource, err = b.output(ctx, kind, "inspect", id, "--format", `{{index .Config.Labels "point.resource"}}`)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			// Shared binary volumes are removed only when no container refers to them.
			if strings.TrimSpace(resource) == "tools" {
				if helpers[id] {
					continue
				}
				users, err := b.output(ctx, "ps", "-a", "--filter", "volume="+id, "--format", "{{.ID}}")
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if strings.TrimSpace(users) != "" {
					continue
				}
				// Retained sandboxes can recreate the identical helper on their next command.
			}
			if _, err := b.ownedResource(ctx, kind, id, label, strings.TrimSpace(resource)); err != nil {
				errs = append(errs, err)
				continue
			}
			args := []string{kind, "rm"}
			if kind == "container" {
				args = append(args, "--force")
			}
			args = append(args, id)
			if err = b.runInfrastructure(ctx, args...); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
