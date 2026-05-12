# Security policy

## Supported versions

gomedia is pre-1.0. Security fixes land on `master` and are tagged in the next release. There is no extended support window for older tags.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security reports.

Instead, use GitHub's private vulnerability reporting:
<https://github.com/ugparu/gomedia/security/advisories/new>

Include:

- A description of the issue and the impact.
- Steps to reproduce, ideally with a minimal sample stream or fixture.
- The affected package(s) and commit.

We aim to acknowledge reports within 7 days and to ship a fix or mitigation within 90 days of the initial report. Coordinated disclosure timelines can be agreed on a case-by-case basis.

## Scope

In scope:

- Parsers under `codec/`, `format/`, `utils/sdp`, `utils/nal`, `utils/bits` — buffer over-reads, OOB writes, panics on adversarial input, integer overflows.
- Network handling under `format/rtsp` and `writer/webrtc`.
- Resource exhaustion (unbounded allocation, leaked goroutines) under packet ingest.

Out of scope:

- Vulnerabilities in third-party dependencies — report those upstream.
- Misuse where the application disables documented safety guarantees.
