package app

import (
	"context"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Option configures application integrations without expanding the composition
// root with feature-specific setup code.
type Option func(*App)

func WithDirectoryPicker(picker func(context.Context) (string, error)) Option {
	return func(application *App) { application.directoryPicker = picker }
}

func WithEventSink(sink func(domain.Event)) Option {
	return func(application *App) { application.eventSink = sink }
}

func WithSandboxBackend(backend sandbox.Backend) Option {
	return func(application *App) { application.sandboxBackend = backend }
}

func WithSourceFetcher(fetcher SourceFetcher) Option {
	return func(application *App) { application.sourceFetcher = fetcher }
}

func WithGitRunner(runner GitRunner) Option {
	return func(application *App) { application.gitRunner = runner }
}

func WithDeliveredAppRunner(runner DeliveredAppRunner) Option {
	return func(application *App) { application.deliveredAppRunner = runner }
}

func WithCompletionCheckRunner(runner CompletionCheckRunner) Option {
	return func(application *App) { application.completionCheckRunner = runner }
}
