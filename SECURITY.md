# Security Policy

## Supported Versions

Only the latest release is actively maintained with security fixes.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |
| older   | :x:                |

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Use GitHub's [private vulnerability reporting](https://github.com/guanshan/pi-go/security/advisories/new)
to report a vulnerability confidentially.

Include as much detail as you can:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept (if applicable)
- Affected versions or commits
- Any suggested mitigations you are aware of

We aim to acknowledge reports within 5 business days and will keep you informed
of progress toward a fix and public disclosure.

## Scope

This project is a CLI tool that reads API keys from environment variables and
executes shell commands on behalf of the user.  Areas of particular concern:

- Credential handling (`packages/ai` — API keys, OAuth tokens stored in `~/.pi/agent/auth.json`)
- Shell execution (`packages/coding-agent/core/tools` — `bash` tool)
- File I/O tools (`read`, `write`, `edit`) and path traversal
- Session JSONL file parsing
- Extension / package loading from arbitrary git/npm sources

Out of scope: social engineering, physical access, issues in upstream
dependencies (please report those to the relevant project directly).
