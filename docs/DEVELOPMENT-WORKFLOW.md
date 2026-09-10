# Development workflow

Implement authorized changes in /docker/vloader. Subsequent tasks use a feature branch. Run focused tests, full Go checks as relevant, Compose validation and rebuild/restart. Check health and logs before handoff. Keep production separate; tags and releases require an explicit request. Never copy .env into Git or build contexts. Remove only verified disposable task artifacts, preserving volumes and unrelated applications.
