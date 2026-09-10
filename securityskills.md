# vloader Security Workflows

Adapted from caddymgm .agents/securityskills.md; apply to vloader only.

## review-go-security
Trace HTTP input through authentication, origin checks, JSON decoding, URL parsing, file roots, response headers and HTML rendering. Verify state bounds, mutex ownership, error handling, secret redaction and TLS verification. Add meaningful regression tests for credible bypasses.

## review-authentication
Verify login failures and rate limits, session rotation/expiry/logout, secure cookies, CSRF, OIDC state/nonce/PKCE, issuer/audience/signature validation and the subject allowlist. No OIDC identity is admitted solely because an identity provider accepted it.

## review-emby-and-storage
Check pagination, library membership, original item IDs, API-token isolation, redirect refusal and persistence on failed sync. Exercise path traversal, sibling-prefix confusion, symlink escape, directories and file ranges. Confirm mounted sources are read-only and never controlled through a privileged web handler.

## review-containers
Validate Docker configuration without printing substituted secrets. Verify non-root UID, mounts, capabilities, root filesystem, health checks, resource bounds and log rotation. Run go test, race tests, vet and govulncheck; record results. Never delete persistent volumes as cleanup.

## create-threat-model
Map browser, Go service, local credential, OIDC issuer, Emby API and mounted storage. Identify trusted administrators, secrets, SSRF/path/DOM injection boundaries and denial-of-service limits. Distinguish verified findings from improvements and deployment assumptions.

## incident-triage
Preserve logs and timestamps. Avoid printing tokens, cookies or private media metadata. Report evidence, affected scope, severity, remediation and validation. Production actions and external notifications need user authorization.
