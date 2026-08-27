# Changelog

All notable changes to the GraphSentry extension are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.4.0]

### Added

- **Unauthenticated-endpoint detection**: a new deterministic, no-LLM
  security check surfaces HTTP endpoints with no recognized auth guard.
  - CodeLens: endpoints with no guard detected get an inline
    "⚠ No auth guard detected" annotation.
  - New command: **GraphSentry: Security — Endpoints Without an Auth
    Guard**, listing every endpoint and its auth status (protected /
    public / unprotected / unknown).
  - Recognizes ASP.NET's `[Authorize]` / `[AllowAnonymous]`, plus custom
    `TypeFilterAttribute`-backed guards (resolved structurally via the
    code graph, not by attribute name).
  - This is a structural signal, not a vulnerability scan — always verify
    a finding before treating it as one.
- `graphsentry flow` (and the flow panel's diagram) now shows what guards
  an endpoint, as part of the call-flow tree/diagram.

### Fixed

- Mermaid flow diagrams broke ("Syntax error in text") for any endpoint
  whose route contains parameter placeholders like `{id}` — the braces
  weren't escaped in either the node label or the node ID.

## [0.3.0] and earlier

See the [GitHub releases](https://github.com/cloud-ct/graphsentry/releases)
and commit history for changes prior to this changelog's introduction.
