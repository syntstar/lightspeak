# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report them privately through GitHub: open the **Security** tab of this repository and choose **Report a vulnerability** ([direct link](https://github.com/syntstar/lightspeak/security/advisories/new)).

Helpful details:

- which part is affected (Windows app, browser client, server, installer) and which version,
- what an attacker can achieve and what they need for it,
- steps to reproduce, or a proof of concept.

You can expect a first answer within a few days. Lightspeak is maintained in spare time, so please allow some time for a fix before publishing details. Reporters are credited in the release notes unless they prefer otherwise.

## Supported versions

Only the latest release receives security fixes. Apps update themselves; servers are updated with `sudo lightspeak-admin update`.

## Scope

The threat model and known limits are described in [docs/SECURITY.md](../docs/SECURITY.md). In particular, text messages and files are not end-to-end encrypted by design, and the operator of a server is trusted with them.
