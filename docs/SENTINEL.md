# vloader Review Gate

Before a release, assess authentication, authorization, secret handling, Emby input, filesystem confinement, persistence/concurrency, Docker and UI failure states. Record the reviewed revision, commands, results, findings and limitations in docs/VALIDATION.md or a PR review.

Critical/high findings block release. A review is independent only when performed by a separate reviewer; do not label author inspection independent. The upstream dimension prompts in upstream-sentinel/ are optional reference material, not proof that any review has run. Do not claim branch protection or automated review enforcement unless configured and verified.
