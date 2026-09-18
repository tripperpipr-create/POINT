# Go health-service fixture

This small repository is mounted by `docker compose` as the default safe workspace. It is intentionally missing a `/health` route so the acceptance flow can ask the agent to add one, review its patch, approve `go test ./...`, and inspect the full event history.
