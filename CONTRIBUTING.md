# Contributing

Thanks for your interest. Frugal is small and focused — contributions that sharpen the existing wedge are more welcome than ones that expand scope.

## Before you open a PR

- Open an issue first for anything larger than a bug fix or docs tweak.
- Keep changes minimal and scoped to the stated problem.
- Add a test for new behavior. Run `make test` before pushing.
- Match recent commit style (`fix:`, `feat:`, `chore:`, `test:`, `docs:`).
- Sign off every commit with the Developer Certificate of Origin: append a
  `Signed-off-by: Your Name <you@example.com>` trailer (or use `git commit -s`).
  CI blocks PRs missing a sign-off on any commit. See
  [developercertificate.org](https://developercertificate.org/) for the text.

## License

Frugal is licensed under [BUSL 1.1](./LICENSE). By contributing, you agree your contributions are licensed under the same terms. Self-hosted and internal commercial use is permitted; offering Frugal as a competing hosted routing service is not. See the [BUSL FAQ](./LICENSE-BUSL-FAQ.md) for plain-English details.

## What we're looking for

- Bug fixes with regression tests
- Provider integrations (new models, new endpoints) that fit the existing config schema
- Benchmark reproducers — real workloads worth adding to the eval harness
- Documentation improvements that tighten claims (remove hand-waves, add measurements)

## What to skip for now

- Hosted control plane / multi-tenancy features
- ML-based intent classification (the heuristic classifier is sufficient while we validate)
- New policy knobs or routing tiers without a documented use case

## Reporting security issues

Please open a [private security advisory](https://github.com/brainsparker/frugal/security/advisories/new) rather than a public issue.
