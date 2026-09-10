# Testing strategy

Go httptest fixtures exercise local login, cookies, protected routes, CSRF, expiry/logout, rate limits, settings redaction, server switching, Emby token headers/redirect refusal, paginated synchronization, failed-sync atomicity, mounted downloads, byte ranges and traversal/symlink denial. Use race detection for shared-state regressions. Run vet and govulncheck.

External acceptance: configure a real Emby server and verify every expected library, movie metadata/poster, an actual download and failed connection feedback. Configure OIDC and verify allowed/denied subjects and callback URL. Mount NFS or SMB with read-only credentials and verify a real source-prefix mapping. These checks require deployment-specific access and are not implied by mock-based tests.
