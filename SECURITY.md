# Security

## Reporting a vulnerability

Use the repository's **Security → Report a vulnerability** option to send a
private report to the maintainers:

https://github.com/richkeenan/dimsum/security/advisories/new

If private reporting is unavailable, contact [@richkeenan](https://github.com/richkeenan)
to arrange a private channel. Do not put vulnerability details, credentials,
configuration backups, or household query logs in a public issue.

Include the affected version, reproduction steps using synthetic data, expected
and actual behavior, and any proposed fix. Security fixes target the latest
release; check for an update before reporting.

## Deployment boundaries

dimsum is intended for private networks. The default DNS and administration
listeners bind all IPv4 interfaces. Restrict access with host/network firewall
rules. The default dashboard uses HTTP, which does not encrypt passwords,
session cookies, or administrator API tokens. Use a trusted network or terminate
HTTPS at a reverse proxy as described in the [deployment guide](guides/deployment.md).

Administrator API/MCP tokens grant full control. The local control socket also
grants administrative access and relies on filesystem permissions; do not expose
it as an unauthenticated TCP service.

Configuration backups contain credentials. Support bundles can contain local
configuration and, when requested, query history. Review them before sharing.
