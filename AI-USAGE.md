# How AI was used

Per the challenge's AI policy, this documents how AI assisted the work, honestly
and specifically. AI was used heavily and openly -- no attempt was made to disguise
it, because the policy explicitly welcomes AI use and asks for this disclosure.

## Tooling

The project was built with an agentic coding assistant (Claude) driving a
disciplined build loop: plan the work, implement, and -- critically -- **witness
every claim by executing it**. No result in this repo is asserted from reasoning
alone; each was produced by running the real code path (Firecracker, the kernel,
the migration) and reading the actual output.

## What the AI did

- **Mechanism research.** Read the `firecracker-next` source to find the primitives
  that make a sub-30ms blackout possible: `mem_backend` `File`/`Uffd` on
  `/snapshot/load`, `Diff` snapshots with `track_dirty_pages`, and
  `network_overrides` for re-homing the guest NIC on the destination host.
- **Implementation.** Wrote the Go orchestrator (`api/internal/livemigrate`), the
  UDP beacon and collector (`images/`), the build and demo scripts (`scripts/`),
  and the container packaging.
- **Empirical tuning.** Measured the blackout, found the dominant costs (the
  one-time pre-copy Full-snapshot pause, which scales with guest RAM; the UFFD
  per-fault userspace round-trip), and chose the design that keeps every guest
  pause under 30ms: pre-copy convergence + a File mmap backend on shared tmpfs +
  a small guest. All numbers in `bench/` come from real runs.

## What the human directed

- The problem framing, the environment choice (macOS + Lima providing nested KVM),
  the decision to demonstrate on the fast native-KVM path, and review of the
  approach and results.
- One request was explicitly **declined**: a suggestion to insert Unicode
  homoglyphs to make the code appear non-AI-generated. That would deceive the
  evaluator and contradicts this very policy, so the code is plain ASCII and the
  AI use is disclosed here instead. (`scripts/check-ascii.sh` verifies no
  homoglyphs are present.)

## Reproducibility

Every claim is reproducible from the scripts in `scripts/` and the harnesses in
`bench/`. See `README.md` for the exact commands.
