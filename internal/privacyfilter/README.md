# privacyfilter (vendored, secrets-only)

Vendored subset of [packyme/privacy-filter](https://github.com/packyme/privacy-filter)
(MIT, Copyright (c) 2026 PackyMe — see `LICENSE.upstream`).

## What we kept / changed
- **Secrets only.** Upstream's PII layer (`pii.go`: email/phone/national-id/bank-card/IP)
  is **not** vendored. SapaLOQ deliberately lets email/phone/IP pass — only secrets are
  redacted. Rationale: a credential is what makes "email + IP" dangerous (a VPS login); strip
  the secret and the combo is defused.
- **No external dependency.** The gitleaks-TOML loader was removed; we always use the
  built-in rule set, so the package no longer imports `github.com/BurntSushi/toml`.
- **English placeholder.** Hits are replaced with `[SECRET]`.

## Why it exists
SapaLOQ applies this to **every tool result** before it reaches the model/logs/egress.
The AI keeps full access to every tool; only secret *values* in results are masked. So even
if the model is tricked by an injected instruction into reading `~/.ssh/id_rsa` or `.env`,
the secret never actually reaches the model or leaves the host.

**Trade-off (accepted):** a legitimate task that genuinely needs a secret value (e.g. "read the
DB password from .env and use it") will also see `[SECRET]`. This is intentional — see
`docs/ORCHESTRATOR.md`.
